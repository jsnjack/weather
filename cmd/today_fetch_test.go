package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTodayForecastBatches(t *testing.T) {
	for _, n := range []int{1, 25, 200, 201, 441, 529} {
		batches := todayForecastBatches(n)
		next := 0
		for _, batch := range batches {
			size := batch[1] - batch[0]
			if batch[0] != next || size < 1 || size > todayForecastBatchSize {
				t.Fatalf("n=%d: invalid batch %v in %v", n, batch, batches)
			}
			next = batch[1]
		}
		if next != n || len(batches) != (n+todayForecastBatchSize-1)/todayForecastBatchSize {
			t.Fatalf("n=%d: batches %v", n, batches)
		}
	}
}

// todayForecastTransport answers ride forecast batches. The first location of
// every request is sea with rain in the second ride hour.
func todayForecastTransport(t *testing.T, calls *atomic.Int32, start time.Time, status int) http.RoundTripper {
	return locationTransportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		q := r.URL.Query()
		if q.Get("start_date") != "" || q.Get("timeformat") != "unixtime" || q.Get("hourly") != "precipitation,wind_speed_10m,wind_direction_10m" {
			t.Errorf("unexpected ride forecast query: %s", r.URL.RawQuery)
		}
		if status != http.StatusOK {
			return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"60"}}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}
		lats := strings.Split(q.Get("latitude"), ",")
		if len(lats) != len(strings.Split(q.Get("longitude"), ",")) {
			t.Errorf("coordinate lists differ: %s", r.URL.RawQuery)
		}
		entries := make([]todayForecastResponse, len(lats))
		for i := range entries {
			entries[i].Elevation = 5
			for at := start.Add(-24 * time.Hour); at.Before(start.Add(48 * time.Hour)); at = at.Add(time.Hour) {
				rain := 0.0
				if i == 0 && at.Equal(start.Add(time.Hour)) {
					rain = 2
				}
				hourly := &entries[i].Hourly
				hourly.Time = append(hourly.Time, at.Unix())
				hourly.Precipitation = append(hourly.Precipitation, rain)
				hourly.WindSpeed = append(hourly.WindSpeed, 12)
				hourly.WindDirection = append(hourly.WindDirection, 270)
			}
		}
		entries[0].Elevation = 0
		var body []byte
		var err error
		if len(entries) == 1 {
			body, err = json.Marshal(entries[0])
		} else {
			body, err = json.Marshal(entries)
		}
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
}

func resetOpenMeteo(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	previousCache, previousGate, previousTransport := openMeteoBodyCache, openMeteoGate, http.DefaultTransport
	openMeteoBodyCache = newForecastBodyCache()
	openMeteoGate = &requestGate{}
	t.Cleanup(func() {
		openMeteoBodyCache, openMeteoGate, http.DefaultTransport = previousCache, previousGate, previousTransport
	})
}

func TestTodayGrid(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Hour).Add(time.Hour)
	t.Run("batched grid is shared by nearby positions", func(t *testing.T) {
		resetOpenMeteo(t)
		var calls atomic.Int32
		http.DefaultTransport = todayForecastTransport(t, &calls, start, http.StatusOK)
		result, err := runTodayGrid(context.Background(), 52.0907, 5.1214, start, 6, 21, 75, NoProgress)
		if err != nil {
			t.Fatal(err)
		}
		if want := int32(len(todayForecastBatches(21 * 21))); calls.Load() != want {
			t.Fatalf("upstream calls = %d, want %d batches", calls.Load(), want)
		}
		for r, row := range result.Cells {
			for c, cell := range row {
				if cell.NoData {
					t.Fatalf("cell %d,%d has no data", r, c)
				}
			}
		}
		if corner := result.Cells[0][0]; corner.DryHours != 1 || !corner.Sea {
			t.Fatalf("first cell = %+v, want sea with one dry hour", corner)
		}
		if centre := result.Cells[10][10]; centre.DryHours != 6 || centre.WindSpeed != 12 || centre.WindBlowsTo != 90 {
			t.Fatalf("centre cell = %+v", centre)
		}
		if _, err := runTodayGrid(context.Background(), 52.0950, 5.1250, start, 6, 21, 75, NoProgress); err != nil {
			t.Fatal(err)
		}
		if want := int32(len(todayForecastBatches(21 * 21))); calls.Load() != want {
			t.Fatalf("a position 500 m away made %d more calls", calls.Load()-want)
		}
	})
	t.Run("single location response is an object", func(t *testing.T) {
		resetOpenMeteo(t)
		var calls atomic.Int32
		http.DefaultTransport = todayForecastTransport(t, &calls, start, http.StatusOK)
		data, err := getTodayForecastBatch([]latLon{{Lat: 52.09, Lon: 5.12}})
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != 1 || !data[0].IsSea() || len(data[0].Hourly) != 72 || !data[0].Hourly[24].Time.Equal(start) {
			t.Fatalf("unexpected single location forecast: %+v", data)
		}
	})
	t.Run("stalled upstream returns when the wait ends", func(t *testing.T) {
		resetOpenMeteo(t)
		started, release := make(chan struct{}, 1), make(chan struct{})
		defer close(release)
		http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
			// The request has read every swapped global by now; receiving
			// this before cleanup orders those reads before the restore.
			started <- struct{}{}
			<-release
			// 429 ends the request without retries once the test is done.
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}, nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		began := time.Now()
		if _, err := runTodayGrid(ctx, 52.0907, 5.1214, start, 6, 5, 75, NoProgress); err == nil || time.Since(began) > time.Second {
			t.Fatalf("stalled grid returned %v after %s", err, time.Since(began))
		}
		<-started
	})
	t.Run("rate limit is an error, not an empty map", func(t *testing.T) {
		resetOpenMeteo(t)
		var calls atomic.Int32
		http.DefaultTransport = todayForecastTransport(t, &calls, start, http.StatusTooManyRequests)
		if _, err := runTodayGrid(context.Background(), 52.0907, 5.1214, start, 6, 5, 75, NoProgress); err == nil {
			t.Fatal("rate-limited grid returned a result")
		}
	})
}
