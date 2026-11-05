package cmd

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strings"
	"testing"
)

func radarDisc(size int, cx, cy, radius float64) *image.NRGBA {
	mask := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			if math.Hypot(float64(x)+.5-cx, float64(y)+.5-cy) <= radius {
				mask.SetNRGBA(x, y, color.NRGBA{56, 189, 248, 255})
			}
		}
	}
	return mask
}

func TestTraceLocalRadarOutline(t *testing.T) {
	t.Run("disc boundary", func(t *testing.T) {
		outline := traceLocalRadarOutline(radarDisc(localRadarSize, 128, 128, 40), 50)
		if outline == nil {
			t.Fatal("no outline around a 40 px disc")
		}
		inked := 0
		for y := range localRadarSize {
			for x := range localRadarSize {
				if outline.ink[y*localRadarSize+x] < .5 {
					continue
				}
				inked++
				if d := math.Hypot(float64(x)+.5-128, float64(y)+.5-128); d < 37 || d > 43 {
					t.Fatalf("ink at (%d, %d), %.1f px from the centre of a 40 px disc", x, y, d)
				}
			}
		}
		// The circumference is about 250 px; a 1.6 px line covers it at least once.
		if inked < 250 {
			t.Fatalf("only %d ink pixels along the boundary", inked)
		}
	})
	t.Run("tiny speck", func(t *testing.T) {
		speck := image.NewNRGBA(image.Rect(0, 0, localRadarSize, localRadarSize))
		draw.Draw(speck, image.Rect(100, 100, 103, 103), image.NewUniform(color.White), image.Point{}, draw.Src)
		if outline := traceLocalRadarOutline(speck, 50); outline != nil {
			t.Fatal("a one-cell speck drew a ring")
		}
	})
	t.Run("rain everywhere", func(t *testing.T) {
		full := image.NewNRGBA(image.Rect(0, 0, localRadarSize, localRadarSize))
		draw.Draw(full, full.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		if outline := traceLocalRadarOutline(full, 50); outline != nil {
			t.Fatal("rain across the whole map drew a line along its edge")
		}
	})
	t.Run("rain crossing the edge", func(t *testing.T) {
		half := image.NewNRGBA(image.Rect(0, 0, localRadarSize, localRadarSize))
		draw.Draw(half, image.Rect(0, 0, 100, localRadarSize), image.NewUniform(color.White), image.Point{}, draw.Src)
		outline := traceLocalRadarOutline(half, 50)
		if outline == nil {
			t.Fatal("no line at the rain's edge")
		}
		for y := range localRadarSize {
			if outline.halo[y*localRadarSize] != 0 || outline.ink[y*localRadarSize+100] < .5 && outline.ink[y*localRadarSize+99] < .5 {
				t.Fatalf("row %d: line along the map edge or missing at the rain edge", y)
			}
		}
	})
	t.Run("larger map scales the line", func(t *testing.T) {
		small := traceLocalRadarOutline(radarDisc(localRadarSize, 128, 128, 40), 50)
		large := traceLocalRadarOutline(radarDisc(localRadarLargeSize, 256, 256, 80), 50)
		if small == nil || large == nil {
			t.Fatal("missing outline")
		}
		width := func(o *localRadarOutline, row int) (ink, halo float64) {
			for x := range o.size {
				ink += o.ink[row*o.size+x]
				halo += o.halo[row*o.size+x]
			}
			return ink, halo
		}
		smallInk, smallHalo := width(small, 128)
		largeInk, largeHalo := width(large, 256)
		// Each row crosses the line twice, once on each side of the disc.
		if math.Abs(smallInk/2-localRadarOutlineInkWidth) > .3 || math.Abs(largeInk/smallInk-2) > .2 || math.Abs(largeHalo/smallHalo-2) > .2 {
			t.Fatalf("ink %.2f→%.2f, halo %.2f→%.2f", smallInk, largeInk, smallHalo, largeHalo)
		}
	})
}

func TestLocalRadarOutlineColors(t *testing.T) {
	outline := traceLocalRadarOutline(radarDisc(localRadarSize, 128, 128, 40), 50)
	for _, tc := range []struct {
		name       string
		dark       bool
		ink        color.NRGBA
		background color.RGBA
	}{
		{"light", false, lightRadarOutlineInk, color.RGBA{201, 202, 203, 255}},
		{"dark", true, darkRadarOutlineInk, color.RGBA{28, 35, 27, 255}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canvas := image.NewRGBA(image.Rect(0, 0, localRadarSize, localRadarSize))
			draw.Draw(canvas, canvas.Bounds(), image.NewUniform(tc.background), image.Point{}, draw.Src)
			outline.draw(canvas, tc.dark)
			if canvas.RGBAAt(128, 128) != tc.background || canvas.RGBAAt(5, 5) != tc.background {
				t.Fatal("outline painted away from the rain's edge")
			}
			distance := func(c color.RGBA, to color.RGBA) float64 {
				return math.Abs(float64(c.R)-float64(to.R)) + math.Abs(float64(c.G)-float64(to.G)) + math.Abs(float64(c.B)-float64(to.B))
			}
			ink := color.RGBA{tc.ink.R, tc.ink.G, tc.ink.B, 255}
			inked := false
			for x := 160; x <= 176; x++ {
				c := canvas.RGBAAt(x, 128)
				inked = inked || distance(c, ink) < distance(c, tc.background)/2
			}
			if !inked {
				t.Fatal("no ink colour along the rain's edge")
			}
		})
	}
}

func TestUpscaleRadarLabels(t *testing.T) {
	source := image.NewRGBA(image.Rect(256, 512, 512, 768))
	text := color.RGBA{40, 40, 40, 255}
	draw.Draw(source, image.Rect(300, 600, 340, 640), image.NewUniform(text), image.Point{}, draw.Src)
	draw.Draw(source, image.Rect(400, 600, 401, 640), image.NewUniform(color.RGBA{255, 255, 255, 255}), image.Point{}, draw.Src)
	result := upscaleRadarLabels(source, 256, 512, 256, 512)
	if result.Bounds() != image.Rect(0, 0, 512, 512) {
		t.Fatalf("bounds=%v", result.Bounds())
	}
	if got := result.RGBAAt(2*320-256*2, 2*620-512*2); got != text {
		t.Errorf("label interior=%v, want %v", got, text)
	}
	if got := result.RGBAAt(20, 20); got != (color.RGBA{}) {
		t.Errorf("transparent surroundings=%v", got)
	}
	for i := 0; i < len(result.Pix); i += 4 {
		if p := result.Pix[i : i+4]; p[0] > p[3] || p[1] > p[3] || p[2] > p[3] {
			t.Fatalf("pixel %d is not valid premultiplied colour: %v", i/4, p)
		}
	}
}

func TestRadarCommandRejectsAnimatedOptions(t *testing.T) {
	oldOutput, oldAnimate, oldSize, oldOutline, oldRadius := radarOutput, radarAnimate, radarSize, radarOutline, radarRadius
	t.Cleanup(func() {
		radarOutput, radarAnimate, radarSize, radarOutline, radarRadius = oldOutput, oldAnimate, oldSize, oldOutline, oldRadius
	})
	for _, tc := range []struct {
		name    string
		animate bool
		size    int
		outline bool
		want    string
	}{
		{"unsupported size", false, 300, false, "--size must be 256 or 512"},
		{"large animation", true, 512, false, "not to --animate"},
		{"outlined animation", true, 256, true, "not to --animate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			radarOutput, radarAnimate, radarSize, radarOutline, radarRadius = "radar.png", tc.animate, tc.size, tc.outline, 50
			err := radarCmd.RunE(radarCmd, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}
}
