package cmd

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDarkLocalRadar(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		name := "labelled map"
		if fallback {
			name = "embedded map fallback"
		}
		t.Run(name, func(t *testing.T) {
			overlay := image.NewRGBA(image.Rect(0, 0, 256, 256))
			rain := color.RGBA{40, 40, 40, 255}
			overlay.SetRGBA(50, 50, rain)
			overlay.SetRGBA(60, 60, color.RGBA{255, 0, 0, 255})
			labels := image.NewNRGBA(overlay.Bounds())
			labels.SetNRGBA(70, 70, color.NRGBA{0, 0, 0, 255})
			x, y := radarMercator(52.37, 4.9)
			radar := &localRadarImage{overlay: overlay, background: image.NewUniform(color.White), labels: labels, x: x, y: y, halfWidth: localRadarRadiusM / math.Cos(52.37*math.Pi/180)}
			if fallback {
				radar.background, radar.labels = nil, nil
			}
			before := bytes.Clone(overlay.Pix)
			light, err := renderLocalRadarImageTheme(radar, 52.37, 4.9, false)
			if err != nil {
				t.Fatal(err)
			}
			dark, err := renderLocalRadarImageTheme(radar, 52.37, 4.9, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, point := range []image.Point{{50, 50}, {60, 60}, {128, 128}, {135, 128}} {
				if dark.RGBAAt(point.X, point.Y) != light.RGBAAt(point.X, point.Y) {
					t.Errorf("weather/marker colour changed at %v", point)
				}
			}
			base := dark.RGBAAt(0, 0)
			if base == light.RGBAAt(0, 0) || max(base.R, base.G, base.B) > 70 {
				t.Fatal("map background remained light")
			}
			if !fallback && dark.RGBAAt(70, 70).R < 200 {
				t.Fatal("place label is unreadable in dark mode")
			}
			if !bytes.Equal(before, overlay.Pix) || labels.NRGBAAt(70, 70) != (color.NRGBA{0, 0, 0, 255}) {
				t.Fatal("shared source layers were mutated")
			}
		})
	}
}

func TestDarkRadarLayerAlpha(t *testing.T) {
	for _, labels := range []bool{false, true} {
		name := "background"
		if labels {
			name = "labels"
		}
		t.Run(name, func(t *testing.T) {
			source := image.NewNRGBA(image.Rect(0, 0, 3, 1))
			for x, a := range []uint8{0, 128, 255} {
				source.SetNRGBA(x, 0, color.NRGBA{40, 120, 150, a})
			}
			before := bytes.Clone(source.Pix)
			result := darkRadarLayer(source, labels)
			for x, a := range []uint8{0, 128, 255} {
				if got := color.NRGBAModel.Convert(result.At(x, 0)).(color.NRGBA).A; got != a {
					t.Errorf("alpha=%d, want %d", got, a)
				}
			}
			if !bytes.Equal(before, source.Pix) {
				t.Fatal("cached layer changed")
			}
		})
	}
}

func TestDarkBakedMapLabels(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{0, 0, 0, 255})
	source.SetNRGBA(1, 0, color.NRGBA{165, 194, 225, 255})
	result := darkRadarLayer(source, false)
	if color.NRGBAModel.Convert(result.At(0, 0)).(color.NRGBA).R < 200 {
		t.Fatal("baked town names remain too dim")
	}
	if color.NRGBAModel.Convert(result.At(1, 0)).(color.NRGBA).B < 200 {
		t.Fatal("water labels blend into dark water")
	}
}

func TestDarkNationalRadar(t *testing.T) {
	palette := color.Palette{color.RGBA{53, 146, 186, 255}, color.Black, color.RGBA{255, 0, 0, 255}, color.White, color.RGBA{113, 165, 76, 255}, color.RGBA{143, 183, 109, 255}, color.RGBA{135, 163, 174, 255}}
	observed := image.NewPaletted(image.Rect(0, 0, 425, 445), palette)
	observed.SetColorIndex(50, 50, 6)
	observed.SetColorIndex(60, 60, 2)
	observed.SetColorIndex(80, 400, 1)
	observed.SetColorIndex(100, 100, 4)
	observed.SetColorIndex(110, 110, 5)
	for i := range 300 {
		observed.SetColorIndex(250+i%175, 429+i/175, 1)
	}
	predicted := image.NewPaletted(image.Rect(0, 409, 425, 445), palette)
	for i := range 300 {
		predicted.SetColorIndex(250+i%175, 429+i/175, 2)
	}
	animation := &gif.GIF{Image: []*image.Paletted{observed, predicted}, Delay: []int{10, 15}, Disposal: []byte{gif.DisposalNone, gif.DisposalBackground}, Config: image.Config{ColorModel: palette, Width: 425, Height: 445}, LoopCount: 0}
	var body bytes.Buffer
	if err := gif.EncodeAll(&body, animation); err != nil {
		t.Fatal(err)
	}
	oldMaps, oldDark, oldTimelines := radarMapCache, darkRadarMapCache, radarTimelineCache
	t.Cleanup(func() { radarMapCache, darkRadarMapCache, radarTimelineCache = oldMaps, oldDark, oldTimelines })
	radarMapCache = newTTLCache[[]byte](time.Minute, 1)
	darkRadarMapCache = newTTLCache[[]byte](time.Minute, 1)
	radarTimelineCache = newTTLCache[*radarTimeline](time.Minute, 4)
	radarMapCache.put("knmi", body.Bytes())
	before := bytes.Clone(body.Bytes())
	light, err := getRadarTimelineTheme(false)
	if err != nil {
		t.Fatal(err)
	}
	dark, err := getRadarTimelineTheme(true)
	if err != nil {
		t.Fatal(err)
	}
	if light.AtlasURL == dark.AtlasURL || dark.LatestObservation == nil || *dark.LatestObservation != 0 {
		t.Fatal("theme broke snapshot identity or observation detection")
	}
	decoded, err := png.Decode(bytes.NewReader(dark.atlas))
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range []image.Point{{50, 50}, {60, 60}, {80, 400}} {
		if color.RGBAModel.Convert(decoded.At(point.X, point.Y)) != color.RGBAModel.Convert(observed.At(point.X, point.Y)) {
			t.Errorf("weather colour changed at %v", point)
		}
	}
	if got := color.RGBAModel.Convert(decoded.At(0, 0)); got != (color.RGBA{17, 39, 52, 255}) {
		t.Errorf("sea colour=%v", got)
	}
	if got := color.RGBAModel.Convert(decoded.At(250, 429)); got != (color.RGBA{225, 225, 225, 255}) {
		t.Errorf("timestamp unreadable: %v", got)
	}
	gifBytes, err := getKNMIRadarMapTheme(true)
	if err != nil {
		t.Fatal(err)
	}
	converted, err := gif.DecodeAll(bytes.NewReader(gifBytes))
	if err != nil {
		t.Fatal(err)
	}
	if converted.Image[1].Bounds() != predicted.Bounds() || converted.Delay[1] != 15 || converted.Disposal[1] != gif.DisposalBackground || converted.LoopCount != 0 {
		t.Fatal("dark GIF changed playback or frame offsets")
	}
	if color.RGBAModel.Convert(converted.Image[0].At(50, 50)) != color.RGBAModel.Convert(observed.At(50, 50)) {
		t.Fatal("GIF fallback changed rain")
	}
	if !bytes.Equal(before, body.Bytes()) {
		t.Fatal("cached original GIF changed")
	}
	unknown := image.NewRGBA(image.Rect(0, 0, 20, 20))
	draw.Draw(unknown, unknown.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	if darkNationalRadarImage(unknown) != unknown {
		t.Fatal("unrecognised national map was recoloured")
	}
}

func TestRadarThemeValidation(t *testing.T) {
	for _, handler := range []struct {
		name string
		fn   http.HandlerFunc
	}{
		{"local", handleLocalRadar}, {"local timeline", handleLocalRadarTimeline}, {"local stream", handleLocalRadarStream}, {"national", handleRadarTimeline}, {"national fallback", handleRadarMap},
	} {
		t.Run(handler.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.fn(w, httptest.NewRequest("GET", "/?theme=invalid", nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}

func TestDarkRadarPNGEncoding(t *testing.T) {
	x, y := radarMercator(52.37, 4.9)
	mapData := &localRadarImage{overlay: image.NewRGBA(image.Rect(0, 0, 256, 256)), background: image.NewUniform(color.White), x: x, y: y, halfWidth: 50000}
	body, err := renderLocalRadarTheme(mapData, 52.37, 4.9, true)
	if err != nil {
		t.Fatal(err)
	}
	picture, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if picture.Bounds() != image.Rect(0, 0, 256, 256) {
		t.Fatal("dark PNG changed widget geometry")
	}
	if _, err := getLocalRadarTimelineTheme(context.Background(), 0, 0, true); err != nil {
		t.Fatal("dark theme broke unsupported-coverage fallback")
	}
}
