package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const openMeteoRequestInterval = 250 * time.Millisecond

var openMeteoHTTP = &http.Client{Timeout: 15 * time.Second}
var openMeteoRequests = make(chan struct{}, multidayFetchWorkers)
var openMeteoGate = &requestGate{}
var openMeteoBodyCache = newForecastBodyCache()

func newForecastBodyCache() *ttlCache[storedForecast] {
	cache := newTTLCache[storedForecast](openMeteoDailyTTL, forecastStoreLimit)
	cache.expires = func(entry storedForecast) time.Time { return entry.Expires }
	return cache
}

type requestGate struct {
	mu       sync.Mutex
	next     time.Time
	cooldown time.Time
}

func (g *requestGate) wait() error {
	for {
		g.mu.Lock()
		now := time.Now()
		if now.Before(g.cooldown) {
			until := g.cooldown
			g.mu.Unlock()
			return fmt.Errorf("open-meteo rate limited until %s", until.Format(time.RFC3339))
		}
		delay := g.next.Sub(now)
		if delay <= 0 {
			g.next = now.Add(openMeteoRequestInterval)
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()
		time.Sleep(delay)
	}
}

func (g *requestGate) pause(until time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if until.After(g.cooldown) {
		g.cooldown = until
	}
}

func retryAfter(value string, now time.Time) time.Time {
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 && seconds <= 86400 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date
	}
	return now.Add(time.Minute)
}

func openMeteoGetBody(url string) ([]byte, error) {
	entry, err := memo(openMeteoBodyCache, url, func() (storedForecast, error) {
		return loadOpenMeteoBody(url)
	})
	return entry.Body, err
}

func loadOpenMeteoBody(url string) (storedForecast, error) {
	path, pathErr := forecastStorePath(url)
	if pathErr != nil {
		slog.Debug("forecast disk cache unavailable", "err", pathErr)
	}
	if pathErr == nil {
		if entry, ok := readStoredForecast(path, time.Now()); ok {
			return entry, nil
		}
	}
	body, err := fetchOpenMeteoBody(url, openMeteoHTTP, openMeteoGate)
	if err != nil {
		return storedForecast{}, err
	}
	if err := validateOpenMeteoBody(url, body); err != nil {
		return storedForecast{}, err
	}
	ttl := openMeteoHourlyTTL
	if !strings.Contains(url, "&hourly=") {
		ttl = openMeteoDailyTTL
	}
	expires := time.Now().Add(ttl)
	if strings.Contains(url, "forecast_days=") {
		var metadata struct {
			Timezone string `json:"timezone"`
			Offset   int    `json:"utc_offset_seconds"`
		}
		if err := json.Unmarshal(body, &metadata); err == nil {
			now := time.Now().In(openMeteoZone(metadata.Timezone, metadata.Offset))
			midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
			if midnight.Before(expires) {
				expires = midnight
			}
		}
	}
	entry := storedForecast{Body: body, Expires: expires}
	if pathErr == nil {
		if err := writeStoredForecast(path, entry); err != nil {
			slog.Debug("save forecast disk cache", "err", err)
		}
	}
	return entry, nil
}

func fetchOpenMeteoBody(url string, client *http.Client, gate *requestGate) ([]byte, error) {
	openMeteoRequests <- struct{}{}
	defer func() { <-openMeteoRequests }()
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if err := gate.wait(); err != nil {
			return nil, err
		}
		slog.Debug("open-meteo: requesting", "url", url, "attempt", attempt+1)
		response, err := client.Get(url)
		if err != nil {
			lastErr = fmt.Errorf("request open-meteo: %w", err)
		} else if response.StatusCode == http.StatusTooManyRequests {
			until := retryAfter(response.Header.Get("Retry-After"), time.Now())
			gate.pause(until)
			closeBody(response.Body, "open-meteo rate-limited response")
			return nil, fmt.Errorf("open-meteo rate limited until %s", until.Format(time.RFC3339))
		} else if response.StatusCode >= 500 {
			lastErr = fmt.Errorf("open-meteo transient status %d", response.StatusCode)
			closeBody(response.Body, "open-meteo transient response")
		} else {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
			closeBody(response.Body, "open-meteo response body")
			if readErr != nil {
				return nil, fmt.Errorf("read open-meteo response: %w", readErr)
			}
			if response.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("open-meteo status %d: %s", response.StatusCode, body)
			}
			return body, nil
		}
		if attempt < 3 {
			time.Sleep(time.Duration(1<<attempt) * time.Second)
		}
	}
	return nil, fmt.Errorf("open-meteo retries exhausted: %w", lastErr)
}

func validateOpenMeteoBody(requestURL string, body []byte) error {
	var response struct {
		Error  bool                       `json:"error"`
		Reason string                     `json:"reason"`
		Hourly map[string]json.RawMessage `json:"hourly"`
		Daily  map[string]json.RawMessage `json:"daily"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("decode open-meteo response: %w", err)
	}
	if response.Error {
		return fmt.Errorf("open-meteo: %s", response.Reason)
	}
	parsedURL, err := url.Parse(requestURL)
	if err != nil {
		return fmt.Errorf("parse open-meteo URL: %w", err)
	}
	for _, section := range []struct {
		name   string
		fields map[string]json.RawMessage
	}{{"hourly", response.Hourly}, {"daily", response.Daily}} {
		requested := parsedURL.Query().Get(section.name)
		if requested == "" {
			continue
		}
		var times []string
		if err := json.Unmarshal(section.fields["time"], &times); err != nil || len(times) == 0 {
			return fmt.Errorf("open-meteo response has no %s times", section.name)
		}
		for _, field := range strings.Split(requested, ",") {
			var values []json.RawMessage
			if err := json.Unmarshal(section.fields[field], &values); err != nil || len(values) != len(times) {
				return fmt.Errorf("open-meteo returned inconsistent %s array lengths (%s)", section.name, field)
			}
		}
	}
	return nil
}
