package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
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

func TestLocalRadarStream(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail bool
	}{
		{"first frame before forecasts and cached replay", false},
		{"partial failure returns an error event", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRadarBasemap(t)
			// Frames load in parallel; authenticated pacing keeps the six requests short.
			t.Setenv("KNMI_WMS_API_KEY", "test")
			oldObs, oldMaps, oldRun, oldFrames, oldRendered := radarObservationCache, localRadarCache, radarForecastRunCache, radarForecastFrameCache, localRadarRenderedFrameCache
			t.Cleanup(func() {
				radarObservationCache, localRadarCache, radarForecastRunCache, radarForecastFrameCache, localRadarRenderedFrameCache = oldObs, oldMaps, oldRun, oldFrames, oldRendered
				radarWMSGate.Lock()
				radarWMSGate.next = time.Time{}
				radarWMSGate.cooldown = time.Time{}
				radarWMSGate.Unlock()
			})
			radarObservationCache = newTTLCache[time.Time](time.Minute, 1)
			localRadarCache = newTTLCache[*localRadarImage](time.Minute, 4)
			radarForecastRunCache = newTTLCache[time.Time](time.Minute, 1)
			radarForecastFrameCache = newTTLCache[image.Image](time.Minute, 256)
			localRadarRenderedFrameCache = newTTLCache[[]byte](time.Minute, 256)
			observed := time.Now().UTC().Truncate(5 * time.Minute)
			x, y := radarMercator(52.37, 4.9)
			overlay := image.NewRGBA(image.Rect(0, 0, 256, 256))
			overlay.SetRGBA(150, 128, color.RGBA{255, 0, 0, 255})
			radarObservationCache.put("latest", observed)
			localRadarCache.put(fmt.Sprintf("%.2f,%.2f,50,%s", 52.37, 4.9, observed.Format(time.RFC3339)), &localRadarImage{
				overlay: overlay, observed: observed, x: x, y: y, halfWidth: localRadarRadiusM / math.Cos(52.37*math.Pi/180), radiusKM: 50,
			})
			gate := make(chan struct{})
			var release sync.Once
			unlock := func() { release.Do(func() { close(gate) }) }
			var calls atomic.Int32
			http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				var body []byte
				status := 200
				if r.URL.Query().Get("REQUEST") == "GetCapabilities" {
					body = fmt.Appendf(nil, `<WMS_Capabilities><Capability><Layer><Layer><Name>precipitation_nowcast</Name><Dimension name="reference_time" default="%s"/></Layer></Layer></Capability></WMS_Capabilities>`, observed.Format(time.RFC3339))
				} else {
					select {
					case <-gate:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
					if tc.fail {
						status = 429
					} else {
						var encoded bytes.Buffer
						source := image.NewRGBA(image.Rect(0, 0, localRadarSourceSize, localRadarSourceSize))
						source.SetRGBA(278, 256, color.RGBA{255, 0, 0, 255})
						if err := png.Encode(&encoded, source); err != nil {
							return nil, err
						}
						body = encoded.Bytes()
					}
				}
				radarWMSGate.Lock()
				radarWMSGate.next = time.Time{}
				radarWMSGate.Unlock()
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
			})
			server := httptest.NewServer(http.HandlerFunc(handleLocalRadarTimeline))
			defer func() { unlock(); server.Close() }()
			// Keep the local test connection outside the mocked upstream transport.
			client := http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			query := "/api/v1/radar/local/timeline?lat=52.37&lon=4.9&label=Amsterdam&progressive=1"
			response, err := client.Get(server.URL + query)
			if err != nil {
				t.Fatal(err)
			}
			defer closeBody(response.Body, "test radar stream")
			if response.Header.Get("Content-Type") != "application/x-ndjson" || response.Header.Get("X-Accel-Buffering") != "no" {
				t.Fatal("stream may be buffered by proxy")
			}
			decoder := json.NewDecoder(response.Body)
			var event struct {
				Type  string
				Index int
				Image string
				Times []time.Time
			}
			if err := decoder.Decode(&event); err != nil || event.Type != "metadata" || len(event.Times) != 13 {
				t.Fatalf("missing early metadata: %v", err)
			}
			if err := decoder.Decode(&event); err != nil || event.Type != "frame" || event.Index != 0 {
				t.Fatalf("observation waited for blocked forecasts: %v", err)
			}
			body, err := base64.StdEncoding.DecodeString(event.Image[len("data:image/png;base64,"):])
			if err != nil {
				t.Fatal(err)
			}
			if _, err := png.Decode(bytes.NewReader(body)); err != nil {
				t.Fatal(err)
			}
			unlock()
			frames := 1
			for {
				if err := decoder.Decode(&event); err != nil {
					t.Fatal(err)
				}
				if event.Type == "error" {
					if !tc.fail {
						t.Fatal("valid forecast failed")
					}
					break
				}
				if event.Type == "complete" {
					if tc.fail || frames != 13 {
						t.Fatal("incomplete stream claimed completion")
					}
					break
				}
				if event.Type != "frame" || event.Index != frames {
					t.Fatal("frames are out of order")
				}
				frames++
			}
			if tc.fail {
				if _, err := getLocalRadar(context.Background(), 52.37, 4.9); err != nil {
					t.Fatal("observation lost after failed forecast")
				}
				return
			}
			if calls.Load() != 13 {
				t.Fatalf("upstream requests=%d, want 13", calls.Load())
			}
			response2, err := client.Get(server.URL + query)
			if err != nil {
				t.Fatal(err)
			}
			defer closeBody(response2.Body, "test radar replay")
			if _, err := io.Copy(io.Discard, response2.Body); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 13 {
				t.Fatal("replay refetched upstream frames")
			}
		})
	}
}
