package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestForecastStore(t *testing.T) {
	t.Run("persists without extending freshness", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "cache", "forecast.json")
		expires := time.Now().Add(time.Minute)
		entry := storedForecast{Expires: expires, Body: []byte(`{"timezone":"Europe/Amsterdam"}`)}
		if err := writeStoredForecast(path, entry); err != nil {
			t.Fatal(err)
		}
		got, ok := readStoredForecast(path, time.Now())
		if !ok || !got.Expires.Equal(expires) || string(got.Body) != string(entry.Body) {
			t.Fatalf("stored result = %v, %v", got, ok)
		}
		if _, ok := readStoredForecast(path, expires); ok {
			t.Fatal("expired response reused")
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("mode = %v", info.Mode())
		}
	})
	t.Run("corrupt cache is ignored", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, ok := readStoredForecast(path, time.Now()); ok {
			t.Fatal("corrupt response reused")
		}
	})
}

func TestRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, value string
		want        time.Time
	}{
		{"seconds", "120", now.Add(2 * time.Minute)},
		{"date", now.Add(3 * time.Minute).Format(http.TimeFormat), now.Add(3 * time.Minute)},
		{"missing", "", now.Add(time.Minute)},
		{"invalid", "no", now.Add(time.Minute)},
		{"past", now.Add(-time.Minute).Format(http.TimeFormat), now.Add(time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryAfter(tc.value, now); !got.Equal(tc.want) {
				t.Fatalf("retryAfter = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOpenMeteoCooldown(t *testing.T) {
	t.Run("429 blocks subsequent requests across callers", func(t *testing.T) {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()
		gate := &requestGate{}
		for range 2 {
			if _, err := fetchOpenMeteoBody(server.URL, server.Client(), gate); err == nil {
				t.Fatal("expected rate-limit error")
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("requests during cooldown = %d", calls.Load())
		}
	})
	t.Run("disk cache survives memory cache reset", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			if _, err := w.Write([]byte(`{"timezone":"Europe/Amsterdam","value":1}`)); err != nil {
				t.Error(err)
			}
		}))
		defer server.Close()
		previousCache, previousGate := openMeteoBodyCache, openMeteoGate
		openMeteoBodyCache = newForecastBodyCache()
		openMeteoGate = &requestGate{}
		t.Cleanup(func() { openMeteoBodyCache = previousCache; openMeteoGate = previousGate })
		for range 2 {
			if _, err := openMeteoGetBody(server.URL); err != nil {
				t.Fatal(err)
			}
			openMeteoBodyCache = newForecastBodyCache()
		}
		if calls.Load() != 1 {
			t.Fatalf("persistent cache made %d upstream calls", calls.Load())
		}
	})
}

func TestOpenMeteoResponseValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		fail       bool
	}{
		{"valid", `{"hourly":{"time":["2026-10-10T00:00"],"temperature_2m":[12]}}`, false},
		{"ragged", `{"hourly":{"time":["2026-10-10T00:00"],"temperature_2m":[]}}`, true},
		{"error response", `{"error":true,"reason":"quota exceeded"}`, true},
		{"empty", "{}", true},
		{"invalid JSON", "bad", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOpenMeteoBody("https://api.open-meteo.com/v1/forecast?hourly=temperature_2m", []byte(tc.body))
			if (err != nil) != tc.fail {
				t.Fatalf("validation error = %v", err)
			}
		})
	}
}
