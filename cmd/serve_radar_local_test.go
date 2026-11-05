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
	"strings"
	"sync"
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
		got := cropLocalRadarOverlay(source, radius, localRadarSize)
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

var localRadarStubBasemap = color.RGBA{201, 202, 203, 255}

// localRadarStubRain is the observation's rain class after recolouring.
var localRadarStubRain = color.RGBA{220, 38, 38, 255}

// localRadarUpstream serves KNMI observations, forecasts and PDOK tiles from
// memory and counts the requests of each kind.
type localRadarUpstream struct {
	observed, reference               time.Time
	rain, forecastRain                bool
	observationStatus, forecastStatus int
	mu                                sync.Mutex
	calls                             map[string]int
}

func (u *localRadarUpstream) count(kind string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls[kind]
}

func (u *localRadarUpstream) tiles() map[string]int {
	u.mu.Lock()
	defer u.mu.Unlock()
	tiles := make(map[string]int)
	for kind, n := range u.calls {
		if strings.HasPrefix(kind, "/") {
			tiles[kind] = n
		}
	}
	return tiles
}

func stubLocalRadarUpstream(t *testing.T, upstream *localRadarUpstream) {
	t.Helper()
	resetRadarBasemap(t)
	t.Setenv("KNMI_WMS_API_KEY", "test")
	oldObs, oldMaps, oldSources, oldRun, oldFrames := radarObservationCache, localRadarCache, localRadarSourceCache, radarForecastRunCache, radarForecastFrameCache
	t.Cleanup(func() {
		radarObservationCache, localRadarCache, localRadarSourceCache, radarForecastRunCache, radarForecastFrameCache = oldObs, oldMaps, oldSources, oldRun, oldFrames
		radarWMSGate.Lock()
		radarWMSGate.next = time.Time{}
		radarWMSGate.cooldown = time.Time{}
		radarWMSGate.Unlock()
	})
	radarObservationCache = newTTLCache[time.Time](time.Minute, 1)
	localRadarCache = newTTLCache[*localRadarImage](time.Minute, 8)
	localRadarSourceCache = newTTLCache[image.Image](time.Minute, 4)
	radarForecastRunCache = newTTLCache[time.Time](time.Minute, 1)
	radarForecastFrameCache = newTTLCache[image.Image](time.Minute, 8)
	if upstream.reference.IsZero() {
		upstream.reference = upstream.observed
	}
	upstream.calls = make(map[string]int)
	encode := func(picture image.Image) []byte {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, picture); err != nil {
			t.Fatal(err)
		}
		return encoded.Bytes()
	}
	http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		status := http.StatusOK
		var kind string
		var body []byte
		switch {
		case r.URL.Host == "service.pdok.nl":
			kind = r.URL.Path
			tile := image.NewRGBA(image.Rect(0, 0, radarTileSize, radarTileSize))
			if strings.Contains(r.URL.Path, "/grijs/") {
				draw.Draw(tile, tile.Bounds(), image.NewUniform(localRadarStubBasemap), image.Point{}, draw.Src)
			}
			body = encode(tile)
		case q.Get("DATASET") == radarForecastDataset && q.Get("REQUEST") == "GetCapabilities":
			kind = "forecast capabilities"
			body = fmt.Appendf(nil, `<WMS_Capabilities><Capability><Layer><Layer><Name>precipitation_nowcast</Name><Dimension name="reference_time" default="%s"/></Layer></Layer></Capability></WMS_Capabilities>`, upstream.reference.Format(time.RFC3339))
		case q.Get("DATASET") == radarForecastDataset:
			kind = "forecast map"
			if q.Get("TIME") != upstream.observed.Add(localRadarOutlineLead).Format(time.RFC3339) || q.Get("DIM_REFERENCE_TIME") != upstream.reference.Format(time.RFC3339) || q.Get("LAYERS") != "precipitation_nowcast" {
				t.Errorf("outline forecast is not pinned to 30 minutes in the run: %v", q)
			}
			if upstream.forecastStatus != 0 {
				status = upstream.forecastStatus
				break
			}
			// A 40 km disc of rain around the map's centre.
			frame := image.NewRGBA(image.Rect(0, 0, localRadarSourceSize, localRadarSourceSize))
			if upstream.forecastRain {
				for y := range localRadarSourceSize {
					for x := range localRadarSourceSize {
						if math.Hypot(float64(x)+.5-256, float64(y)+.5-256) <= 40 {
							frame.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
						}
					}
				}
			}
			body = encode(frame)
		case q.Get("REQUEST") == "GetCapabilities":
			kind = "observation capabilities"
			if upstream.observationStatus != 0 {
				status = upstream.observationStatus
				break
			}
			body = fmt.Appendf(nil, `<WMS_Capabilities xmlns="http://www.opengis.net/wms"><Capability><Layer><Layer><Name>precipitation</Name><Dimension name="time" default="%s"/></Layer></Layer></Capability></WMS_Capabilities>`, upstream.observed.Format(time.RFC3339))
		default:
			kind = "observation map"
			// The 100 km source pixel that the 50 km map shows at (150, 128).
			overlay := image.NewRGBA(image.Rect(0, 0, localRadarSourceSize, localRadarSourceSize))
			if upstream.rain {
				overlay.SetRGBA(278, 256, color.RGBA{255, 0, 0, 255})
			}
			body = encode(overlay)
		}
		upstream.mu.Lock()
		upstream.calls[kind]++
		upstream.mu.Unlock()
		radarWMSGate.Lock()
		radarWMSGate.next = time.Time{}
		radarWMSGate.Unlock()
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
}

func containsColor(picture image.Image, want color.RGBA) bool {
	bounds := picture.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if color.RGBAModel.Convert(picture.At(x, y)) == want {
				return true
			}
		}
	}
	return false
}

func TestLocalRadarAlways(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		age                    time.Duration
		rain                   bool
		observationStatus      int
		query                  string
		status                 int
		observed               bool
		radarStatus, radarRain string
	}{
		{"dry map", 5 * time.Minute, false, 0, "always=1", 200, true, "", "none"},
		{"rainy map", 5 * time.Minute, true, 0, "always=true", 200, true, "", "nearby"},
		{"stale observation", 25 * time.Minute, true, 0, "always=1", 200, false, "unavailable", "none"},
		{"KNMI failure", 5 * time.Minute, true, 503, "always=1", 200, false, "unavailable", "none"},
		{"legacy rainy map", 5 * time.Minute, true, 0, "", 200, true, "", "nearby"},
		{"legacy dry map", 5 * time.Minute, false, 0, "always=0", 204, false, "", ""},
		{"legacy stale observation", 25 * time.Minute, true, 0, "always=false", 204, false, "", ""},
		{"legacy KNMI failure", 5 * time.Minute, true, 503, "", 502, false, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &localRadarUpstream{observed: time.Now().Add(-tc.age).UTC().Truncate(5 * time.Minute), rain: tc.rain, observationStatus: tc.observationStatus}
			stubLocalRadarUpstream(t, upstream)
			w := httptest.NewRecorder()
			handleLocalRadar(w, httptest.NewRequest("GET", "/api/v1/radar/local?lat=52.37&lon=4.90&label=Amsterdam&"+tc.query, nil))
			if w.Code != tc.status {
				t.Fatalf("status=%d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			wantObserved := ""
			if tc.observed {
				wantObserved = upstream.observed.Format(time.RFC3339)
			}
			for header, want := range map[string]string{"X-Radar-Observed-At": wantObserved, "X-Radar-Status": tc.radarStatus, "X-Radar-Rain": tc.radarRain, "X-Radar-Outline": ""} {
				if got := w.Header().Get(header); got != want {
					t.Errorf("%s=%q, want %q", header, got, want)
				}
			}
			if w.Code != 200 {
				if tiles := upstream.tiles(); tc.status == 204 && len(tiles) > 0 {
					t.Errorf("an empty response fetched map tiles: %v", tiles)
				}
				return
			}
			picture, err := png.Decode(bytes.NewReader(w.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if picture.Bounds() != image.Rect(0, 0, localRadarSize, localRadarSize) {
				t.Fatalf("bounds=%v", picture.Bounds())
			}
			if got := color.RGBAModel.Convert(picture.At(2, 2)); got != localRadarStubBasemap {
				t.Errorf("map background=%v, want the PDOK basemap", got)
			}
			if got, want := containsColor(picture, localRadarStubRain), tc.radarRain == "nearby"; got != want {
				t.Errorf("rain drawn=%t, want %t", got, want)
			}
		})
	}
}

func TestLocalRadarOptionsRequest(t *testing.T) {
	for query, want := range map[string]localRadarOptions{
		"":                                 {radiusKM: 50, size: 256},
		"size=256&always=0&theme=light":    {radiusKM: 50, size: 256},
		"size=512&always=1&outline=30":     {radiusKM: 50, size: 512, always: true, outline: true},
		"always=true&theme=dark&radius=25": {radiusKM: 25, size: 256, dark: true, always: true},
		"always=false&size=512&radius=100": {radiusKM: 100, size: 512},
	} {
		got, err := localRadarOptionsRequest(httptest.NewRequest(http.MethodGet, "/api/v1/radar/local?"+query, nil))
		if err != nil || got != want {
			t.Errorf("%q = %+v, %v; want %+v", query, got, err, want)
		}
	}
	for _, query := range []string{"size=128", "size=1024", "size=512px", "always=yes", "always=2", "outline=15", "outline=true", "outline=30m", "theme=sepia", "radius=75"} {
		t.Run(query, func(t *testing.T) {
			old := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = old })
			http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
				t.Errorf("invalid request reached %s", r.URL.Host)
				return nil, fmt.Errorf("unexpected upstream request")
			})
			w := httptest.NewRecorder()
			handleLocalRadar(w, httptest.NewRequest(http.MethodGet, "/api/v1/radar/local?lat=52.37&lon=4.90&label=Amsterdam&"+query, nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400", w.Code)
			}
		})
	}
}

func TestLocalRadarOutsideCoverage(t *testing.T) {
	upstream := &localRadarUpstream{observed: time.Now().UTC().Truncate(5 * time.Minute), rain: true}
	stubLocalRadarUpstream(t, upstream)
	w := httptest.NewRecorder()
	handleLocalRadar(w, httptest.NewRequest(http.MethodGet, "/api/v1/radar/local?lat=0&lon=0&label=Origin&always=1&size=512&outline=30", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", w.Code)
	}
	if len(upstream.calls) != 0 {
		t.Fatalf("queried upstream outside coverage: %v", upstream.calls)
	}
}

func TestLocalRadarLargeSize(t *testing.T) {
	upstream := &localRadarUpstream{observed: time.Now().UTC().Truncate(5 * time.Minute), rain: true}
	stubLocalRadarUpstream(t, upstream)
	for _, size := range []int{localRadarLargeSize, localRadarSize} {
		w := httptest.NewRecorder()
		handleLocalRadar(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/radar/local?lat=52.37&lon=4.90&label=Amsterdam&size=%d", size), nil))
		if w.Code != 200 || w.Header().Get("X-Radar-Rain") != "nearby" {
			t.Fatalf("%d px: status=%d rain=%q", size, w.Code, w.Header().Get("X-Radar-Rain"))
		}
		picture, err := png.Decode(bytes.NewReader(w.Body.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		if picture.Bounds() != image.Rect(0, 0, size, size) {
			t.Fatalf("bounds=%v, want %d px", picture.Bounds(), size)
		}
		scale := size / localRadarSize
		// One KNMI source pixel covers scale × scale map pixels.
		for _, point := range []image.Point{{150 * scale, 128 * scale}, {150*scale + scale - 1, 128*scale + scale - 1}} {
			if got := color.RGBAModel.Convert(picture.At(point.X, point.Y)); got != localRadarStubRain {
				t.Errorf("%d px rain at %v=%v, want %v", size, point, got, localRadarStubRain)
			}
		}
		if got := color.RGBAModel.Convert(picture.At(128*scale+4*scale, 128*scale)); got != (color.RGBA{6, 90, 130, 255}) {
			t.Errorf("%d px marker edge=%v; the marker should scale with the map", size, got)
		}
	}
	if n := upstream.count("observation map"); n != 1 {
		t.Errorf("KNMI map requests=%d, want one shared by both sizes", n)
	}
	zooms := make(map[string]bool)
	for path := range upstream.tiles() {
		parts := strings.Split(path, "/")
		zooms[parts[len(parts)-5]+"/"+parts[len(parts)-3]] = true
	}
	for _, want := range []string{"grijs/08", "grijs/09", "labels/08"} {
		if !zooms[want] {
			t.Errorf("no %s tiles in %v", want, zooms)
		}
	}
	if zooms["labels/09"] {
		t.Error("512 px labels came from a closer zoom and would shrink relative to the map")
	}
}

func TestLocalRadarOutlineResponse(t *testing.T) {
	for _, tc := range []struct {
		name           string
		forecastRain   bool
		forecastStatus int
		referenceAge   time.Duration
		outlined       bool
	}{
		{"forecast rain", true, 0, 0, true},
		{"dry forecast", false, 0, 0, false},
		{"forecast failure", true, 500, 0, false},
		{"stale forecast run", true, 0, time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observed := time.Now().UTC().Truncate(5 * time.Minute)
			upstream := &localRadarUpstream{observed: observed, reference: observed.Add(-tc.referenceAge), forecastRain: tc.forecastRain, forecastStatus: tc.forecastStatus}
			stubLocalRadarUpstream(t, upstream)
			w := httptest.NewRecorder()
			handleLocalRadar(w, httptest.NewRequest(http.MethodGet, "/api/v1/radar/local?lat=52.37&lon=4.90&label=Amsterdam&always=1&outline=30", nil))
			if w.Code != 200 || w.Header().Get("X-Radar-Observed-At") == "" {
				t.Fatalf("the outline changed the map response: status=%d", w.Code)
			}
			if got := w.Header().Get("X-Radar-Outline") == "30"; got != tc.outlined {
				t.Fatalf("X-Radar-Outline=%q, want outlined=%t", w.Header().Get("X-Radar-Outline"), tc.outlined)
			}
			picture, err := png.Decode(bytes.NewReader(w.Body.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			// The forecast's 40 km disc ends 40 px east of the centre.
			inked := false
			for x := 164; x <= 172; x++ {
				c := color.RGBAModel.Convert(picture.At(x, 128)).(color.RGBA)
				inked = inked || max(c.R, c.G, c.B) < 110
			}
			if inked != tc.outlined {
				t.Errorf("ink drawn=%t, want %t", inked, tc.outlined)
			}
		})
	}
}

func TestLocalRadarWidgetRequestsShareUpstream(t *testing.T) {
	upstream := &localRadarUpstream{observed: time.Now().UTC().Truncate(5 * time.Minute), rain: true, forecastRain: true}
	stubLocalRadarUpstream(t, upstream)
	bodies := make(map[string][]byte)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, theme := range []string{"light", "dark"} {
		wg.Go(func() {
			w := httptest.NewRecorder()
			handleLocalRadar(w, httptest.NewRequest(http.MethodGet, "/api/v1/radar/local?lat=52.37&lon=4.90&label=Amsterdam&always=1&size=512&outline=30&theme="+theme, nil))
			if w.Code != 200 || w.Header().Get("X-Radar-Outline") != "30" || w.Header().Get("X-Radar-Rain") != "nearby" {
				t.Errorf("%s: status=%d headers=%v", theme, w.Code, w.Header())
			}
			mu.Lock()
			bodies[theme] = w.Body.Bytes()
			mu.Unlock()
		})
	}
	wg.Wait()
	if bytes.Equal(bodies["light"], bodies["dark"]) {
		t.Error("light and dark maps share one rendering")
	}
	for _, kind := range []string{"observation capabilities", "observation map", "forecast capabilities", "forecast map"} {
		if n := upstream.count(kind); n != 1 {
			t.Errorf("%s requests=%d, want 1", kind, n)
		}
	}
	for path, n := range upstream.tiles() {
		if n != 1 {
			t.Errorf("tile %s requested %d times", path, n)
		}
	}
}
