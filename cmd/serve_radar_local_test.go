package cmd

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLocalRadarFreshness(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		age  time.Duration
		want bool
	}{
		{"recent observation", 5 * time.Minute, true}, {"old observation", 21 * time.Minute, false},
		{"clock tolerance", -time.Minute, true}, {"future forecast", -3 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := freshRadarObservation(now.Add(-tc.age), now); got != tc.want {
				t.Fatalf("fresh=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestLocalRadarRainAndMarker(t *testing.T) {
	overlay := image.NewRGBA(image.Rect(0, 0, localRadarSize, localRadarSize))
	if hasLocalRadarRain(overlay) {
		t.Fatal("empty radar should fall back")
	}
	overlay.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	if hasLocalRadarRain(overlay) {
		t.Fatal("rain outside 50 km circle should fall back")
	}
	overlay.SetRGBA(150, 128, color.RGBA{255, 0, 0, 255})
	if !hasLocalRadarRain(overlay) {
		t.Fatal("missed nearby rain")
	}
	x, y := radarMercator(52.37, 4.9)
	radar := &localRadarImage{overlay: overlay, x: x, y: y, halfWidth: localRadarRadiusM / math.Cos(52.37*math.Pi/180)}
	body, err := renderLocalRadar(radar, 52.37, 4.9)
	if err != nil {
		t.Fatal(err)
	}
	picture, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if got := color.RGBAModel.Convert(picture.At(128, 128)); got != (color.RGBA{6, 90, 130, 255}) {
		t.Fatalf("location marker=%v", got)
	}
	if got := color.RGBAModel.Convert(picture.At(150, 128)); got != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("rain pixel=%v", got)
	}
}

func TestLocalRadarResponses(t *testing.T) {
	for _, tc := range []struct {
		name          string
		age           time.Duration
		rain          bool
		query         string
		status, calls int
	}{
		{"nearby rain", 5 * time.Minute, true, "lat=52.37&lon=4.90&label=Amsterdam", 200, 2},
		{"blank radar", 5 * time.Minute, false, "lat=52.37&lon=4.90&label=Amsterdam", 204, 2},
		{"stale radar", 21 * time.Minute, true, "lat=52.37&lon=4.90&label=Amsterdam", 204, 1},
		{"outside coverage", 5 * time.Minute, true, "lat=0&lon=0&label=Origin", 204, 0},
		{"invalid coordinates", 5 * time.Minute, true, "lat=NaN&lon=4.90", 400, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRadarBasemap(t)
			oldTransport, oldObservations, oldMaps, oldSources := http.DefaultTransport, radarObservationCache, localRadarCache, localRadarSourceCache
			t.Cleanup(func() {
				http.DefaultTransport, radarObservationCache, localRadarCache, localRadarSourceCache = oldTransport, oldObservations, oldMaps, oldSources
				radarWMSGate.Lock()
				radarWMSGate.next = time.Time{}
				radarWMSGate.cooldown = time.Time{}
				radarWMSGate.Unlock()
			})
			radarObservationCache = newTTLCache[time.Time](time.Minute, 1)
			localRadarCache = newTTLCache[*localRadarImage](time.Minute, 4)
			localRadarSourceCache = newTTLCache[image.Image](time.Minute, 4)
			calls := 0
			observed := time.Now().Add(-tc.age).UTC().Truncate(time.Second)
			http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host == "service.pdok.nl" {
					return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}, nil
				}
				calls++
				var body []byte
				if r.URL.Query().Get("REQUEST") == "GetCapabilities" {
					body = fmt.Appendf(nil, `<WMS_Capabilities xmlns="http://www.opengis.net/wms"><Capability><Layer><Layer><Name>precipitation</Name><Dimension name="time" default="%s"/></Layer></Layer></Capability></WMS_Capabilities>`, observed.Format(time.RFC3339))
				} else {
					if got := r.URL.Query().Get("TIME"); got != observed.Format(time.RFC3339) {
						t.Errorf("requested forecast time %s", got)
					}
					if got := r.URL.Query().Get("CRS"); got != "EPSG:3857" {
						t.Errorf("projection=%s", got)
					}
					// The 100 km source pixel that the 50 km map shows at (150, 128).
					overlay := image.NewRGBA(image.Rect(0, 0, localRadarSourceSize, localRadarSourceSize))
					if tc.rain {
						overlay.SetRGBA(278, 256, color.RGBA{255, 0, 0, 255})
					}
					var encoded bytes.Buffer
					if err := png.Encode(&encoded, overlay); err != nil {
						t.Fatal(err)
					}
					body = encoded.Bytes()
				}
				// Keep unit tests independent of the real request pacing clock.
				radarWMSGate.Lock()
				radarWMSGate.next = time.Time{}
				radarWMSGate.Unlock()
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
			})
			for i := range 2 {
				if i == 1 {
					radarObservationCache = newTTLCache[time.Time](time.Minute, 1)
					localRadarCache = newTTLCache[*localRadarImage](time.Minute, 4)
					localRadarSourceCache = newTTLCache[image.Image](time.Minute, 4)
				}
				w := httptest.NewRecorder()
				handleLocalRadar(w, httptest.NewRequest("GET", "/api/v1/radar/local?"+tc.query, nil))
				if w.Code != tc.status {
					t.Fatalf("status=%d: %s", w.Code, w.Body.String())
				}
				if w.Code == 200 {
					if got := w.Header().Get("X-Radar-Observed-At"); got != observed.Format(time.RFC3339) {
						t.Fatalf("observed=%s", got)
					}
					if _, err := png.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
						t.Fatal(err)
					}
				}
			}
			if calls != tc.calls {
				t.Fatalf("upstream calls=%d, want %d", calls, tc.calls)
			}
		})
	}
}

func TestLocalRadarRateLimit(t *testing.T) {
	for _, status := range []int{403, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			old := http.DefaultTransport
			t.Cleanup(func() {
				http.DefaultTransport = old
				radarWMSGate.Lock()
				radarWMSGate.next = time.Time{}
				radarWMSGate.cooldown = time.Time{}
				radarWMSGate.Unlock()
			})
			calls := 0
			http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"120"}}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
			})
			for range 2 {
				if _, err := radarWMSRequest(context.Background(), make(map[string][]string)); err == nil {
					t.Fatal("accepted unavailable response")
				}
			}
			if calls != 1 {
				t.Fatalf("called unavailable upstream %d times", calls)
			}
			radarWMSGate.Lock()
			until := radarWMSGate.cooldown
			radarWMSGate.Unlock()
			if time.Until(until) < 110*time.Second {
				t.Fatal("ignored Retry-After")
			}
		})
	}
}

func TestLocalRadarRadiusRequest(t *testing.T) {
	for query, want := range map[string]int{"": 50, "radius=25": 25, "radius=50": 50, "radius=100": 100} {
		got, err := localRadarRadiusRequest(httptest.NewRequest(http.MethodGet, "/api/v1/radar/local?"+query, nil))
		if err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", query, got, err, want)
		}
	}
	for _, query := range []string{"radius=0", "radius=75", "radius=50.0", "radius=abc"} {
		if _, err := localRadarRadiusRequest(httptest.NewRequest(http.MethodGet, "/api/v1/radar/local?"+query, nil)); err == nil {
			t.Errorf("%q accepted", query)
		}
	}
}

func TestLocalRadarShown(t *testing.T) {
	dry := image.NewRGBA(image.Rect(0, 0, 256, 256))
	rainy := image.NewRGBA(image.Rect(0, 0, 256, 256))
	rainy.SetRGBA(128, 128, color.RGBA{56, 189, 248, 255})
	now := time.Now()
	for name, test := range map[string]struct {
		radar *localRadarImage
		want  bool
	}{
		"missing":       {nil, false},
		"default dry":   {&localRadarImage{overlay: dry, observed: now, radiusKM: 50}, false},
		"default rainy": {&localRadarImage{overlay: rainy, observed: now, radiusKM: 50}, true},
		"zoomed dry":    {&localRadarImage{overlay: dry, observed: now, radiusKM: 25}, true},
		"zoomed stale":  {&localRadarImage{overlay: rainy, observed: now.Add(-time.Hour), radiusKM: 100}, false},
	} {
		if got := test.radar.shown(); got != test.want {
			t.Errorf("%s = %t, want %t", name, got, test.want)
		}
	}
}

func TestCropLocalRadarOverlay(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, localRadarSourceSize, localRadarSourceSize))
	rain := color.RGBA{56, 189, 248, 255}
	// A 1 km KNMI cell 10 km east of the centre, at 2.56 source pixels per km.
	draw.Draw(source, image.Rect(281, 255, 284, 258), image.NewUniform(rain), image.Point{}, draw.Src)
	for radius, x := range map[int]int{25: 179, 50: 153, 100: 140} {
		got := cropLocalRadarOverlay(source, radius)
		if got.Bounds() != image.Rect(0, 0, localRadarSize, localRadarSize) {
			t.Fatalf("%d km bounds=%v", radius, got.Bounds())
		}
		if c := color.RGBAModel.Convert(got.At(x, 128)); c != rain {
			t.Errorf("%d km pixel (%d, 128)=%v, want rain", radius, x, c)
		}
	}
}

func TestLocalRadarZoomSharesKNMIRequest(t *testing.T) {
	resetRadarBasemap(t)
	oldTransport, oldObservations, oldMaps, oldSources := http.DefaultTransport, radarObservationCache, localRadarCache, localRadarSourceCache
	t.Cleanup(func() {
		http.DefaultTransport, radarObservationCache, localRadarCache, localRadarSourceCache = oldTransport, oldObservations, oldMaps, oldSources
		radarWMSGate.Lock()
		radarWMSGate.next = time.Time{}
		radarWMSGate.Unlock()
	})
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	radarObservationCache = newTTLCache[time.Time](time.Minute, 1)
	localRadarCache = newTTLCache[*localRadarImage](time.Minute, 4)
	localRadarSourceCache = newTTLCache[image.Image](time.Minute, 4)
	observed := time.Now().Add(-5 * time.Minute).UTC().Truncate(time.Second)
	maps := 0
	http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "service.pdok.nl" {
			return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}
		var body []byte
		if r.URL.Query().Get("REQUEST") == "GetCapabilities" {
			body = fmt.Appendf(nil, `<WMS_Capabilities xmlns="http://www.opengis.net/wms"><Capability><Layer><Layer><Name>precipitation</Name><Dimension name="time" default="%s"/></Layer></Layer></Capability></WMS_Capabilities>`, observed.Format(time.RFC3339))
		} else {
			maps++
			if r.URL.Query().Get("WIDTH") != "512" {
				t.Errorf("source width=%s", r.URL.Query().Get("WIDTH"))
			}
			var encoded bytes.Buffer
			if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, localRadarSourceSize, localRadarSourceSize))); err != nil {
				return nil, err
			}
			body = encoded.Bytes()
		}
		radarWMSGate.Lock()
		radarWMSGate.next = time.Time{}
		radarWMSGate.Unlock()
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	for _, radius := range localRadarRadiiKM {
		radar, err := getLocalRadarRadius(context.Background(), 52.37, 4.9, radius)
		if err != nil {
			t.Fatal(err)
		}
		if radar.radiusKM != radius || radar.shown() != (radius != 50) {
			t.Errorf("%d km: radius=%d shown=%t", radius, radar.radiusKM, radar.shown())
		}
	}
	if maps != 1 {
		t.Errorf("KNMI map requests=%d, want one shared by every zoom", maps)
	}
}
