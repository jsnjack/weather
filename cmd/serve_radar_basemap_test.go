package cmd

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func resetRadarBasemap(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	oldTransport, oldCache := http.DefaultTransport, radarBasemapCache
	radarBasemapGate.Lock()
	oldCooldown := radarBasemapGate.cooldown
	radarBasemapGate.cooldown = time.Time{}
	radarBasemapGate.Unlock()
	radarBasemapCache = newTTLCache[image.Image](radarBasemapTTL, 128)
	t.Cleanup(func() {
		http.DefaultTransport, radarBasemapCache = oldTransport, oldCache
		radarBasemapGate.Lock()
		radarBasemapGate.cooldown = oldCooldown
		radarBasemapGate.Unlock()
	})
}

func TestRadarBasemapAlignmentAndCache(t *testing.T) {
	resetRadarBasemap(t)
	var calls atomic.Int32
	http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.Host != "service.pdok.nl" {
			t.Errorf("unexpected tile host %s", r.URL.Host)
		}
		parts := strings.Split(r.URL.Path, "/")
		col, err := strconv.Atoi(parts[len(parts)-2])
		if err != nil {
			t.Fatal(err)
		}
		row, err := strconv.Atoi(strings.TrimSuffix(parts[len(parts)-1], ".png"))
		if err != nil {
			t.Fatal(err)
		}
		tile := image.NewRGBA(image.Rect(0, 0, radarTileSize, radarTileSize))
		for y := range radarTileSize {
			for x := range radarTileSize {
				tile.SetRGBA(x, y, color.RGBA{uint8(col), uint8(row), 200, 255})
			}
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, tile); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(encoded.Bytes()))}, nil
	})
	x, y := radarMercator(52.37, 4.90)
	radar := &localRadarImage{x: x, y: y, halfWidth: localRadarRadiusM / math.Cos(52.37*math.Pi/180)}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			background := localRadarBasemap(context.Background(), radar, "grijs")
			if background == nil {
				t.Error("valid tiles did not produce a map")
				return
			}
			if got := background.At(128, 128); got != (color.RGBA{131, 84, 200, 255}) {
				t.Errorf("wrong map centre: %v", got)
			}
		})
	}
	wg.Wait()
	first := calls.Load()
	if first < 1 || first > 9 {
		t.Fatalf("shared tile requests=%d", first)
	}
	// Removing the memory cache must still reuse the persisted tiles.
	radarBasemapCache = newTTLCache[image.Image](radarBasemapTTL, 128)
	if localRadarBasemap(context.Background(), radar, "grijs") == nil {
		t.Fatal("disk-cached map unavailable")
	}
	if calls.Load() != first {
		t.Fatal("map was downloaded again after memory cache reset")
	}
}

func TestRadarBasemapFailureFallback(t *testing.T) {
	for _, status := range []int{403, 429, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			resetRadarBasemap(t)
			var calls atomic.Int32
			http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": {"120"}}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
			})
			x, y := radarMercator(52.37, 4.90)
			radar := &localRadarImage{x: x, y: y, halfWidth: localRadarRadiusM / math.Cos(52.37*math.Pi/180)}
			for range 2 {
				if localRadarBasemap(context.Background(), radar, "grijs") != nil {
					t.Fatal("failed tiles should use embedded fallback")
				}
			}
			if calls.Load() < 1 || calls.Load() > 2 {
				t.Fatalf("upstream calls during cooldown=%d", calls.Load())
			}
		})
	}
}

func TestRadarBasemapRejectsBadTiles(t *testing.T) {
	for _, size := range []int{0, 128, 512} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			var body bytes.Buffer
			if size > 0 {
				if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, size, size))); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := decodeRadarTile(body.Bytes()); err == nil {
				t.Fatal("accepted corrupt or incorrectly sized tile")
			}
		})
	}
}

func TestRadarBasemapLayerOrder(t *testing.T) {
	for _, wantLabels := range []bool{false, true} {
		name := "rain above roads"
		if wantLabels {
			name = "labels above rain"
		}
		t.Run(name, func(t *testing.T) {
			overlay := image.NewRGBA(image.Rect(0, 0, localRadarSize, localRadarSize))
			overlay.SetRGBA(50, 50, color.RGBA{255, 0, 0, 255})
			x, y := radarMercator(52.37, 4.9)
			radar := &localRadarImage{overlay: overlay, background: image.NewUniform(color.White), x: x, y: y, halfWidth: localRadarRadiusM / math.Cos(52.37*math.Pi/180)}
			want := color.RGBA{255, 0, 0, 255}
			if wantLabels {
				labels := image.NewRGBA(overlay.Bounds())
				want = color.RGBA{0, 0, 200, 255}
				labels.SetRGBA(50, 50, want)
				radar.labels = labels
			}
			body, err := renderLocalRadar(radar, 52.37, 4.9)
			if err != nil {
				t.Fatal(err)
			}
			picture, err := png.Decode(bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			if got := color.RGBAModel.Convert(picture.At(50, 50)); got != want {
				t.Fatalf("layer order pixel=%v, want %v", got, want)
			}
		})
	}
}
