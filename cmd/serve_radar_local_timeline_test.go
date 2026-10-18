package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLocalRadarForecastTimes(t *testing.T) {
	reference := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		offset time.Duration
		count  int
	}{
		{"same run", 0, 13}, {"newer observation", 5 * time.Minute, 12}, {"between steps", 2 * time.Minute, 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := reference.Add(tc.offset)
			times := localRadarForecastTimes(observed, reference)
			if len(times) != tc.count || !times[0].Equal(observed) {
				t.Fatalf("unexpected timeline: %v", times)
			}
			for i, at := range times[1:] {
				if !at.After(times[i]) || at.After(observed.Add(radarForecastHorizon)) || at.After(reference.Add(radarForecastHorizon)) || at.Sub(reference)%radarForecastInterval != 0 {
					t.Fatalf("invalid forecast time: %s", at)
				}
			}
			if reference.Add(radarForecastHorizon).Sub(times[len(times)-1]) >= localRadarForecastStep {
				t.Fatal("timeline ended too early")
			}
			if tc.offset%radarForecastInterval == 0 && times[1].Sub(observed) != localRadarForecastStep {
				t.Fatalf("first forecast %s is not one step after the observation", times[1])
			}
		})
	}
}

func TestLocalRadarForecast(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stale, fail bool
	}{
		{"pinned run and cached replay", false, false}, {"stale reference despite future default time", true, false}, {"rate limited forecast", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRadarBasemap(t)
			// Frames load in parallel; authenticated pacing keeps the six requests short.
			t.Setenv("KNMI_WMS_API_KEY", "test")
			oldObs, oldMaps, oldRun, oldFrames, oldTimeline, oldRendered := radarObservationCache, localRadarCache, radarForecastRunCache, radarForecastFrameCache, localRadarTimelineCache, localRadarRenderedFrameCache
			t.Cleanup(func() {
				radarObservationCache, localRadarCache, radarForecastRunCache, radarForecastFrameCache, localRadarTimelineCache = oldObs, oldMaps, oldRun, oldFrames, oldTimeline
				localRadarRenderedFrameCache = oldRendered
				radarWMSGate.Lock()
				radarWMSGate.next = time.Time{}
				radarWMSGate.cooldown = time.Time{}
				radarWMSGate.Unlock()
			})
			radarObservationCache = newTTLCache[time.Time](time.Minute, 1)
			localRadarCache = newTTLCache[*localRadarImage](time.Minute, 4)
			radarForecastRunCache = newTTLCache[time.Time](time.Minute, 1)
			radarForecastFrameCache = newTTLCache[image.Image](time.Minute, 256)
			localRadarTimelineCache = newTTLCache[*localRadarTimeline](time.Minute, 16)
			localRadarRenderedFrameCache = newTTLCache[[]byte](time.Minute, 256)
			observed := time.Now().UTC().Truncate(5 * time.Minute)
			reference := observed
			if tc.stale {
				reference = observed.Add(-time.Hour)
			}
			x, y := radarMercator(52.37, 4.9)
			overlay := image.NewRGBA(image.Rect(0, 0, 256, 256))
			overlay.SetRGBA(150, 128, color.RGBA{255, 0, 0, 255})
			background := image.NewRGBA(overlay.Bounds())
			labels := image.NewRGBA(overlay.Bounds())
			labels.SetRGBA(150, 128, color.RGBA{0, 0, 0, 255})
			radarObservationCache.put("latest", observed)
			key := fmt.Sprintf("%.2f,%.2f,%s", 52.37, 4.9, observed.Format(time.RFC3339))
			localRadarCache.put(key, &localRadarImage{overlay: overlay, background: background, labels: labels, observed: observed, x: x, y: y, halfWidth: localRadarRadiusM / math.Cos(52.37*math.Pi/180)})
			var calls atomic.Int32
			http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				q := r.URL.Query()
				if q.Get("DATASET") != radarForecastDataset {
					t.Errorf("wrong forecast dataset: %s", q.Get("DATASET"))
				}
				var body []byte
				status := 200
				if q.Get("REQUEST") == "GetCapabilities" {
					body = fmt.Appendf(nil, `<WMS_Capabilities><Capability><Layer><Layer><Name>precipitation_nowcast</Name><Dimension name="time" default="%s"/><Dimension name="reference_time" default="%s"/></Layer></Layer></Capability></WMS_Capabilities>`, observed.Add(2*time.Hour).Format(time.RFC3339), reference.Format(time.RFC3339))
				} else if tc.fail {
					status = 429
				} else {
					if q.Get("DIM_REFERENCE_TIME") != reference.Format(time.RFC3339) || q.Get("CRS") != "EPSG:3857" || q.Get("LAYERS") != "precipitation_nowcast" {
						t.Errorf("unpinned or misaligned frame: %v", q)
					}
					at, err := time.Parse(time.RFC3339, q.Get("TIME"))
					if err != nil {
						return nil, err
					}
					if !at.After(observed) || at.After(observed.Add(radarForecastHorizon)) {
						t.Errorf("forecast outside two hours: %s", at)
					}
					frame := image.NewRGBA(overlay.Bounds())
					frame.SetRGBA(150, 128, color.RGBA{0, 255, 0, 255})
					var b bytes.Buffer
					if err := png.Encode(&b, frame); err != nil {
						return nil, err
					}
					body = b.Bytes()
				}
				radarWMSGate.Lock()
				radarWMSGate.next = time.Time{}
				radarWMSGate.Unlock()
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
			})
			if tc.stale || tc.fail {
				if _, err := getLocalRadarTimeline(context.Background(), 52.37, 4.9); err == nil {
					t.Fatal("unavailable forecast became a playable timeline")
				}
				if _, err := getLocalRadar(context.Background(), 52.37, 4.9); err != nil {
					t.Fatal("forecast failure removed current observation", err)
				}
				if tc.fail {
					before := calls.Load()
					if _, err := getLocalRadarTimeline(context.Background(), 52.37, 4.9); err == nil {
						t.Fatal("cooldown bypassed")
					}
					if calls.Load() != before {
						t.Fatal("retried upstream during cooldown")
					}
				}
				return
			}
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() {
					if _, err := getLocalRadarTimeline(context.Background(), 52.37, 4.9); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if calls.Load() != 13 {
				t.Fatalf("concurrent viewers fetched %d calls, want one metadata + twelve frames", calls.Load())
			}
			timeline, err := getLocalRadarTimeline(context.Background(), 52.37, 4.9)
			if err != nil {
				t.Fatal(err)
			}
			if len(timeline.Times) != 13 || timeline.LatestObservation != 0 || !timeline.Reference.Equal(observed) {
				t.Fatalf("wrong timeline metadata: %+v", timeline)
			}
			dark, err := getLocalRadarTimelineTheme(context.Background(), 52.37, 4.9, true)
			if err != nil {
				t.Fatal(err)
			}
			if dark.AtlasURL == timeline.AtlasURL || calls.Load() != 13 {
				t.Fatal("dark theme refetched weather or reused light snapshot identity")
			}
			atlas, err := png.Decode(bytes.NewReader(timeline.atlas))
			if err != nil {
				t.Fatal(err)
			}
			for _, origin := range []image.Point{{0, 0}, {256, 0}, {0, 768}} {
				if got := color.RGBAModel.Convert(atlas.At(origin.X+128, origin.Y+128)); got != (color.RGBA{6, 90, 130, 255}) {
					t.Fatalf("marker shifted: %v", got)
				}
				if got := color.RGBAModel.Convert(atlas.At(origin.X+150, origin.Y+128)); got != (color.RGBA{0, 0, 0, 255}) {
					t.Fatalf("labels covered by rain: %v", got)
				}
			}
			gifBytes, err := encodeLocalRadarGIF(timeline)
			if err != nil {
				t.Fatal(err)
			}
			animation, err := gif.DecodeAll(bytes.NewReader(gifBytes))
			if err != nil {
				t.Fatal(err)
			}
			if animation.LoopCount != -1 || len(animation.Image) != 13 {
				t.Fatal("GIF should play two complete hours once")
			}
			w := httptest.NewRecorder()
			handleLocalRadarTimeline(w, httptest.NewRequest("GET", "/api/v1/radar/local/timeline?lat=52.37&lon=4.9&label=Amsterdam", nil))
			var decoded localRadarTimeline
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &decoded) != nil || decoded.AtlasURL != timeline.AtlasURL {
				t.Fatal("timeline route did not return cached snapshot")
			}
			w = httptest.NewRecorder()
			handleLocalRadarAtlas(w, httptest.NewRequest("GET", timeline.AtlasURL, nil))
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), timeline.atlas) {
				t.Fatal("atlas route lost the snapshot")
			}
			localRadarTimelineCache = newTTLCache[*localRadarTimeline](time.Minute, 16)
			radarForecastFrameCache = newTTLCache[image.Image](time.Minute, 256)
			radarForecastRunCache = newTTLCache[time.Time](time.Minute, 1)
			localRadarRenderedFrameCache = newTTLCache[[]byte](time.Minute, 256)
			if _, err := getLocalRadarTimeline(context.Background(), 52.37, 4.9); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 13 {
				t.Fatal("disk cache did not survive in-memory reset")
			}
		})
	}
}

func TestLocalRadarTimelineInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		handler    http.HandlerFunc
		status     int
	}{
		{"bad coordinates", "/api/v1/radar/local/timeline?lat=NaN&lon=4.9", handleLocalRadarTimeline, 400},
		{"bad atlas id", "/radar/local-atlas.png?id=bad", handleLocalRadarAtlas, 400},
		{"expired atlas", "/radar/local-atlas.png?id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", handleLocalRadarAtlas, 410},
		{"outside coverage", "/api/v1/radar/local/timeline?lat=0&lon=0&label=Origin", handleLocalRadarTimeline, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handler(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.status {
				t.Fatalf("status=%d, want %d", w.Code, tc.status)
			}
		})
	}
}
