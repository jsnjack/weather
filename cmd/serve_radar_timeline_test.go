package cmd

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRadarObservationMarkers(t *testing.T) {
	for _, tc := range []struct {
		name       string
		bounds     image.Rectangle
		black, red int
		want       radarFrameKind
	}{
		{"observed timestamp", image.Rect(0, 0, 425, 445), 300, 0, radarObserved},
		{"forecast timestamp", image.Rect(0, 0, 425, 445), 0, 300, radarPredicted},
		{"unrecognised caption", image.Rect(0, 0, 425, 445), 0, 0, radarUnknown},
		{"mixed timestamp colours", image.Rect(0, 0, 425, 445), 300, 300, radarUnknown},
		{"changed map size", image.Rect(0, 0, 426, 445), 300, 0, radarUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := image.NewRGBA(tc.bounds)
			draw.Draw(frame, frame.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
			for i := 0; i < tc.black+tc.red; i++ {
				ink := color.RGBA{0, 0, 0, 255}
				if i >= tc.black {
					ink = color.RGBA{250, 5, 2, 255}
				}
				frame.SetRGBA(250+i%175, 429+i/175, ink)
			}
			if got := radarObservationKind(frame); got != tc.want {
				t.Fatalf("kind=%v, want %v", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		frames []radarFrameKind
		want   int
	}{
		{"observation then forecast", []radarFrameKind{radarObserved, radarObserved, radarPredicted}, 1},
		{"only observations", []radarFrameKind{radarObserved, radarObserved}, 1},
		{"only forecasts", []radarFrameKind{radarPredicted}, -1},
		{"unknown frame", []radarFrameKind{radarObserved, radarUnknown, radarPredicted}, -1},
		{"observation after forecast", []radarFrameKind{radarObserved, radarPredicted, radarObserved}, -1},
		{"empty loop", nil, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := latestRadarObservation(tc.frames)
			if tc.want < 0 {
				if got != nil {
					t.Fatalf("observation=%d, want unavailable", *got)
				}
			} else if got == nil || *got != tc.want {
				t.Fatalf("observation=%v, want frame %d", got, tc.want)
			}
		})
	}
}

func radarGIF(t *testing.T, disposal byte) []byte {
	t.Helper()
	palette := color.Palette{color.Black, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{0, 255, 0, 255}}
	first := image.NewPaletted(image.Rect(0, 0, 2, 1), palette)
	first.SetColorIndex(0, 0, 1)
	first.SetColorIndex(1, 0, 1)
	second := image.NewPaletted(image.Rect(0, 0, 1, 1), palette)
	second.SetColorIndex(0, 0, 2)
	third := image.NewPaletted(image.Rect(1, 0, 2, 1), palette)
	third.SetColorIndex(1, 0, 3)
	var body bytes.Buffer
	err := gif.EncodeAll(&body, &gif.GIF{Image: []*image.Paletted{first, second, third}, Delay: []int{0, 20, 30}, Disposal: []byte{gif.DisposalNone, disposal, gif.DisposalNone}, Config: image.Config{ColorModel: palette, Width: 2, Height: 1}})
	if err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestRadarFrameComposition(t *testing.T) {
	for _, tc := range []struct {
		name     string
		disposal byte
		left     color.RGBA
	}{
		{"retain", gif.DisposalNone, color.RGBA{0, 0, 255, 255}},
		{"background", gif.DisposalBackground, color.RGBA{0, 0, 0, 255}},
		{"previous", gif.DisposalPrevious, color.RGBA{255, 0, 0, 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			timeline, err := decodeRadarTimeline(radarGIF(t, tc.disposal))
			if err != nil {
				t.Fatal(err)
			}
			atlas, err := png.Decode(bytes.NewReader(timeline.atlas))
			if err != nil {
				t.Fatal(err)
			}
			if timeline.Columns != 2 || timeline.Width != 2 || timeline.Height != 1 || len(timeline.Delays) != 3 || timeline.Delays[0] != 100 || timeline.Delays[1] != 200 {
				t.Fatalf("metadata=%+v", timeline)
			}
			for _, pixel := range []struct {
				x, y int
				want color.RGBA
			}{{0, 0, color.RGBA{255, 0, 0, 255}}, {2, 0, color.RGBA{0, 0, 255, 255}}, {3, 0, color.RGBA{255, 0, 0, 255}}, {0, 1, tc.left}, {1, 1, color.RGBA{0, 255, 0, 255}}} {
				if got := color.RGBAModel.Convert(atlas.At(pixel.x, pixel.y)); got != pixel.want {
					t.Errorf("pixel %d,%d=%v want %v", pixel.x, pixel.y, got, pixel.want)
				}
			}
		})
	}
	t.Run("malformed GIF", func(t *testing.T) {
		if _, err := decodeRadarTimeline([]byte("invalid")); err == nil {
			t.Fatal("accepted invalid GIF")
		}
	})
}

func TestRadarTimelineRequests(t *testing.T) {
	oldTransport, oldRaw, oldFrames := http.DefaultTransport, radarMapCache, radarTimelineCache
	t.Cleanup(func() { http.DefaultTransport, radarMapCache, radarTimelineCache = oldTransport, oldRaw, oldFrames })
	radarMapCache = newTTLCache[[]byte](3*time.Minute, 1)
	radarTimelineCache = newTTLCache[*radarTimeline](6*time.Minute, 2)
	body := radarGIF(t, gif.DisposalNone)
	requests := 0
	http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})
	var timeline radarTimeline
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		handleRadarTimeline(w, httptest.NewRequest("GET", "/api/v1/radar", nil))
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &timeline); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		handleRadarAtlas(w, httptest.NewRequest("GET", timeline.AtlasURL, nil))
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
			t.Fatal(w.Code)
		}
		if _, err := png.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatalf("upstream requests=%d, want 1", requests)
	}
	t.Run("invalid snapshot", func(t *testing.T) {
		w := httptest.NewRecorder()
		handleRadarAtlas(w, httptest.NewRequest("GET", "/radar/atlas.png?id=bad", nil))
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	})
	t.Run("expired snapshot", func(t *testing.T) {
		radarTimelineCache = newTTLCache[*radarTimeline](6*time.Minute, 2)
		w := httptest.NewRecorder()
		handleRadarAtlas(w, httptest.NewRequest("GET", timeline.AtlasURL, nil))
		if w.Code != 410 {
			t.Fatal(w.Code)
		}
	})
}
