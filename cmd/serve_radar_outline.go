package cmd

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"time"
)

const (
	// localRadarOutlineLead is how far past the observation the outline
	// shows KNMI's nowcast.
	localRadarOutlineLead = 30 * time.Minute
	// localRadarOutlineWait bounds how long a map waits for its outline. The
	// fetch continues, so requests that joined it and the shared caches still
	// receive the frame.
	localRadarOutlineWait = 3 * time.Second
	// Outline widths in pixels on the default map; larger maps scale them.
	localRadarOutlineInkWidth  = 1.6
	localRadarOutlineHaloWidth = 2.0
)

var (
	lightRadarOutlineInk  = color.NRGBA{0x11, 0x18, 0x27, 255}
	lightRadarOutlineHalo = color.NRGBA{255, 255, 255, 230}
	darkRadarOutlineInk   = color.NRGBA{0xf8, 0xfa, 0xfc, 255}
	darkRadarOutlineHalo  = color.NRGBA{0x0a, 0x0c, 0x10, 204}
)

// localRadarOutline holds the anti-aliased coverage, from 0 to 1, of the
// outline's ink and halo for each map pixel. Drawing adds the theme colours.
type localRadarOutline struct {
	size      int
	ink, halo []float64
}

// localRadarOutlineForecast loads the frame of the pinned nowcast run at
// localRadarOutlineLead after the observation, and identifies the run.
func localRadarOutlineForecast(ctx context.Context, radar *localRadarImage) (image.Image, string, error) {
	type result struct {
		frame image.Image
		run   string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), localRadarTimelineTimeout)
		defer cancel()
		reference, err := latestRadarForecastRun(ctx, radar.observed)
		if err != nil {
			done <- result{err: err}
			return
		}
		at := radar.observed.Add(localRadarOutlineLead)
		if !at.After(reference) || at.After(reference.Add(radarForecastHorizon)) || at.Sub(reference)%radarForecastInterval != 0 {
			done <- result{err: fmt.Errorf("radar forecast run %s has no frame at %s", reference.Format(time.RFC3339), at.Format(time.RFC3339))}
			return
		}
		frame, err := localRadarForecastFrame(ctx, radar, reference, at)
		done <- result{frame, reference.Format(time.RFC3339), err}
	}()
	timer := time.NewTimer(localRadarOutlineWait)
	defer timer.Stop()
	select {
	case loaded := <-done:
		return loaded.frame, loaded.run, loaded.err
	case <-timer.C:
		return nil, "", fmt.Errorf("radar outline forecast took longer than %s", localRadarOutlineWait)
	case <-ctx.Done():
		return nil, "", fmt.Errorf("wait for radar outline forecast: %w", ctx.Err())
	}
}

// traceLocalRadarOutline follows the edge of the rain in a forecast overlay
// with a smooth line. Blurring the rain mask by about one KNMI cell rounds
// the 1 km grid's staircase and removes specks too small to outline; the
// line follows the blurred mask's half level, at the distance estimate
// |f − ½| / |∇f|. It returns nil when there is no line to draw.
func traceLocalRadarOutline(forecast image.Image, radiusKM int) *localRadarOutline {
	bounds := forecast.Bounds()
	size := bounds.Dx()
	mask := make([]float64, size*size)
	for y := range size {
		for x := range size {
			if _, _, _, a := forecast.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA(); a >= 0x8000 {
				mask[y*size+x] = 1
			}
		}
	}
	// The map spans twice its radius and KNMI's grid is about 1 km.
	cell := max(1, int(math.Round(float64(size)/float64(2*radiusKM))))
	for range 2 {
		blurRadarMask(mask, size, cell)
	}
	scale := float64(size) / localRadarSize
	inkHalf := localRadarOutlineInkWidth * scale / 2
	haloHalf := inkHalf + localRadarOutlineHaloWidth*scale
	at := func(x, y int) float64 {
		return mask[max(0, min(size-1, y))*size+max(0, min(size-1, x))]
	}
	outline := &localRadarOutline{size: size, ink: make([]float64, size*size), halo: make([]float64, size*size)}
	drawn := false
	for y := range size {
		for x := range size {
			gradient := math.Hypot(at(x+1, y)-at(x-1, y), at(x, y+1)-at(x, y-1)) / 2
			if gradient == 0 {
				continue
			}
			distance := math.Abs(at(x, y)-.5) / gradient
			i := y*size + x
			outline.halo[i] = max(0, min(1, haloHalf+.5-distance))
			outline.ink[i] = max(0, min(1, inkHalf+.5-distance))
			drawn = drawn || outline.ink[i] > 0
		}
	}
	if !drawn {
		return nil
	}
	return outline
}

// blurRadarMask applies a separable box blur of the given radius, repeating
// edge values so rain that crosses the map's edge draws no line there.
func blurRadarMask(mask []float64, size, radius int) {
	line := make([]float64, size)
	window := float64(2*radius + 1)
	blur := func(value func(int) float64, store func(int, float64)) {
		clamp := func(i int) int { return max(0, min(size-1, i)) }
		var sum float64
		for k := -radius; k <= radius; k++ {
			sum += value(clamp(k))
		}
		for i := range size {
			line[i] = sum / window
			sum += value(clamp(i+radius+1)) - value(clamp(i-radius))
		}
		for i, v := range line {
			store(i, v)
		}
	}
	for y := range size {
		blur(func(x int) float64 { return mask[y*size+x] }, func(x int, v float64) { mask[y*size+x] = v })
	}
	for x := range size {
		blur(func(y int) float64 { return mask[y*size+x] }, func(y int, v float64) { mask[y*size+x] = v })
	}
}

func (o *localRadarOutline) draw(canvas *image.RGBA, dark bool) {
	if o == nil {
		return
	}
	ink, halo := lightRadarOutlineInk, lightRadarOutlineHalo
	if dark {
		ink, halo = darkRadarOutlineInk, darkRadarOutlineHalo
	}
	bounds := canvas.Bounds().Intersect(image.Rect(0, 0, o.size, o.size))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			i := y*o.size + x
			if o.halo[i] == 0 {
				continue
			}
			pixel := canvas.Pix[canvas.PixOffset(x, y):]
			blendRadarOutline(pixel, halo, o.halo[i])
			blendRadarOutline(pixel, ink, o.ink[i])
		}
	}
}

// blendRadarOutline paints a colour at the given coverage over a
// premultiplied pixel.
func blendRadarOutline(pixel []uint8, c color.NRGBA, coverage float64) {
	a := coverage * float64(c.A) / 255
	for i, v := range [4]uint8{c.R, c.G, c.B, 255} {
		pixel[i] = uint8(math.Round(float64(v)*a + float64(pixel[i])*(1-a)))
	}
}
