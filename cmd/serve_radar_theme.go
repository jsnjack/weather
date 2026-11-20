package cmd

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"math"
	"net/http"
	"slices"
)

const (
	nationalRadarWidth  = 425
	nationalRadarHeight = 445
)

var (
	darkRadarWater     = color.NRGBA{17, 39, 52, 255}
	darkRadarLand      = color.NRGBA{28, 35, 27, 255}
	darkRadarCountry   = color.NRGBA{38, 46, 35, 255}
	darkRadarText      = color.NRGBA{225, 225, 225, 255}
	darkRadarWaterText = color.NRGBA{132, 195, 215, 255}
)

func darkRadarRequest(r *http.Request) (bool, error) {
	switch r.URL.Query().Get("theme") {
	case "", "light":
		return false, nil
	case "dark":
		return true, nil
	default:
		return false, fmt.Errorf("radar theme must be light or dark")
	}
}

func darkRadarLayer(source image.Image, labels bool, bounds image.Rectangle) image.Image {
	if source == nil {
		return nil
	}
	result := image.NewNRGBA(source.Bounds().Intersect(bounds))
	for y := result.Bounds().Min.Y; y < result.Bounds().Max.Y; y++ {
		for x := result.Bounds().Min.X; x < result.Bounds().Max.X; x++ {
			c := color.NRGBAModel.Convert(source.At(x, y)).(color.NRGBA)
			luminance := (.2126*float64(c.R) + .7152*float64(c.G) + .0722*float64(c.B))
			water := int(c.B)-int(c.R) > 15 && int(c.B)-int(c.G) > 5
			if labels {
				v := uint8(225 - luminance*194/255)
				if water {
					c.R, c.G, c.B = darkRadarWaterText.R, darkRadarWaterText.G, darkRadarWaterText.B
				} else {
					c.R, c.G, c.B = v, v, v
				}
			} else if water {
				if luminance < 215 {
					c.R, c.G, c.B = darkRadarWaterText.R, darkRadarWaterText.G, darkRadarWaterText.B
				} else {
					v := uint8((255 - luminance) * .15)
					c.R, c.G, c.B = 17+v, 39+v, 52+v
				}
			} else {
				v := uint8(18 + (255-luminance)*.8)
				c.R, c.G, c.B = v, v+2, v
			}
			result.SetNRGBA(x, y, c)
		}
	}
	return result
}

func darkNationalRadarColor(c color.Color) color.Color {
	value := color.NRGBAModel.Convert(c).(color.NRGBA)
	// These are KNMI's map fills, not the colours of rain composited over them.
	switch value {
	case color.NRGBA{53, 146, 186, 255}:
		return darkRadarWater
	case color.NRGBA{113, 165, 76, 255}:
		return darkRadarLand
	case color.NRGBA{143, 183, 109, 255}:
		return darkRadarCountry
	default:
		return c
	}
}

func nationalRadarCaption(x, y int, c color.Color) bool {
	return (x < 150 && y >= 409 || x >= 250 && y >= 429) && color.NRGBAModel.Convert(c) == (color.NRGBA{0, 0, 0, 255})
}

var (
	darkKNMIRadarBorder = color.NRGBA{104, 116, 108, 255}
	darkKNMIRadarLabel  = color.NRGBA{32, 37, 30, 255}
)

// The cropped map's sea, land and borders are slate greys, and their
// anti-aliased mixes stay below this chroma. Rain is saturated, so any colour
// above it is weather and keeps its colour.
const knmiRadarMapChroma = 60

// mixRadarColor returns from at t=0 and to at t=1, clamping t.
func mixRadarColor(from, to color.NRGBA, t float64) color.NRGBA {
	t = math.Max(0, math.Min(1, t))
	mix := func(a, b uint8) uint8 { return uint8(math.Round(float64(a) + (float64(b)-float64(a))*t)) }
	return color.NRGBA{mix(from.R, to.R), mix(from.G, to.G), mix(from.B, to.B), 255}
}

// darkKNMIRadarMapColor turns the light map dark by brightness: border lines
// become light lines, land and sea dark fills, and anti-aliased edges fall
// between them. Rain keeps its colour.
func darkKNMIRadarMapColor(c color.RGBA) color.Color {
	if radarChroma(c) > knmiRadarMapChroma {
		return c
	}
	l, sea, land, border := radarLuminance(c), radarLuminance(knmiRadarSea), radarLuminance(knmiRadarLand), radarLuminance(knmiRadarBorder)
	if l >= land {
		return mixRadarColor(darkRadarLand, darkRadarWater, (l-land)/(sea-land))
	}
	return mixRadarColor(darkKNMIRadarBorder, darkRadarLand, (l-border)/(land-border))
}

// darkKNMIRadarLabelColor turns KNMI's light time label dark with light digits.
func darkKNMIRadarLabelColor(c color.RGBA) color.Color {
	if radarChroma(c) > knmiRadarMapChroma {
		return c
	}
	text, fill := radarLuminance(knmiRadarLabelText), radarLuminance(knmiRadarLabelFill)
	return mixRadarColor(darkRadarText, darkKNMIRadarLabel, (radarLuminance(c)-text)/(fill-text))
}

// darkKNMIRadarImage restyles one frame of the cropped map. Temperature circles
// keep KNMI's colours, so their white digits stay readable on the grey disc.
func darkKNMIRadarImage(frame *image.RGBA) *image.RGBA {
	circles := knmiRadarTemperatures(frame)
	result := image.NewRGBA(frame.Bounds())
	for y := frame.Bounds().Min.Y; y < frame.Bounds().Max.Y; y++ {
		for x := frame.Bounds().Min.X; x < frame.Bounds().Max.X; x++ {
			p, c := image.Pt(x, y), frame.RGBAAt(x, y)
			switch {
			case slices.ContainsFunc(circles, func(station image.Point) bool { return inRadarCircle(p, station, knmiRadarStationRadius) }):
				result.SetRGBA(x, y, c)
			case p.In(knmiRadarLabel) && c != knmiRadarSea:
				result.Set(x, y, darkKNMIRadarLabelColor(c))
			default:
				result.Set(x, y, darkKNMIRadarMapColor(c))
			}
		}
	}
	return result
}

// darkKNMIRadarGIF restyles every frame of the cropped map's GIF. Each frame
// gets its own exact palette; a frame with more than 256 colours fails, and the
// caller then serves the light map.
func darkKNMIRadarGIF(animation *gif.GIF) ([]byte, error) {
	config := image.Config{Width: animation.Config.Width, Height: animation.Config.Height}
	dark := &gif.GIF{
		Image:     make([]*image.Paletted, len(animation.Image)),
		Delay:     animation.Delay,
		Disposal:  make([]byte, len(animation.Image)),
		LoopCount: animation.LoopCount,
		Config:    config,
	}
	var err error
	composeGIFFrames(animation, config, func(i int, canvas *image.RGBA) {
		if err != nil {
			return
		}
		frame, ok := exactPaletted(darkKNMIRadarImage(canvas))
		if !ok {
			err = fmt.Errorf("dark radar frame %d has more than 256 colours", i)
			return
		}
		dark.Image[i] = frame
		dark.Disposal[i] = gif.DisposalNone
	})
	if err != nil {
		return nil, err
	}
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, dark); err != nil {
		return nil, fmt.Errorf("encode dark radar GIF: %w", err)
	}
	return encoded.Bytes(), nil
}

// exactPaletted converts an image with at most 256 colours to a paletted one
// without changing any pixel.
func exactPaletted(source *image.RGBA) (*image.Paletted, bool) {
	frame := image.NewPaletted(source.Bounds(), nil)
	indices := map[color.RGBA]uint8{}
	for y := source.Bounds().Min.Y; y < source.Bounds().Max.Y; y++ {
		for x := source.Bounds().Min.X; x < source.Bounds().Max.X; x++ {
			c := source.RGBAAt(x, y)
			index, ok := indices[c]
			if !ok {
				if len(frame.Palette) == 256 {
					return nil, false
				}
				index = uint8(len(frame.Palette))
				indices[c] = index
				frame.Palette = append(frame.Palette, c)
			}
			frame.SetColorIndex(x, y, index)
		}
	}
	return frame, true
}

func darkNationalRadarImage(source image.Image) image.Image {
	if source.Bounds() == knmiRadarCropBounds {
		frame, ok := source.(*image.RGBA)
		if !ok {
			frame = image.NewRGBA(source.Bounds())
			draw.Draw(frame, frame.Bounds(), source, source.Bounds().Min, draw.Src)
		}
		return darkKNMIRadarImage(frame)
	}
	if source.Bounds() != image.Rect(0, 0, nationalRadarWidth, nationalRadarHeight) {
		return source
	}
	result := image.NewRGBA(source.Bounds())
	for y := range nationalRadarHeight {
		for x := range nationalRadarWidth {
			c := source.At(x, y)
			if nationalRadarCaption(x, y, c) {
				result.Set(x, y, darkRadarText)
			} else {
				result.Set(x, y, darkNationalRadarColor(c))
			}
		}
	}
	return result
}

// localRadarRainColors replaces the six classes of KNMI's radar/nearest
// style, lightest first. KNMI paints its lightest class white and the next
// two grey, so light rain disappears on the light basemap. The replacements
// run sky blue, blue, yellow, orange, red and magenta, and read on both
// themes.
var localRadarRainColors = map[color.NRGBA]color.NRGBA{
	{255, 255, 255, 255}: {56, 189, 248, 255},
	{170, 170, 170, 255}: {37, 99, 235, 255},
	{85, 85, 85, 255}:    {250, 204, 21, 255},
	{255, 128, 128, 255}: {249, 115, 22, 255},
	{255, 0, 0, 255}:     {220, 38, 38, 255},
	{0, 0, 0, 255}:       {192, 38, 211, 255},
}

func localRadarRainColor(c color.Color) color.Color {
	if replacement, ok := localRadarRainColors[color.NRGBAModel.Convert(c).(color.NRGBA)]; ok {
		return replacement
	}
	return c
}

// recolorLocalRadarRain applies localRadarRainColors to a KNMI overlay.
// Unknown colours pass through unchanged.
func recolorLocalRadarRain(source image.Image) image.Image {
	if paletted, ok := source.(*image.Paletted); ok {
		palette := make(color.Palette, len(paletted.Palette))
		for i, c := range paletted.Palette {
			palette[i] = localRadarRainColor(c)
		}
		return &image.Paletted{Pix: paletted.Pix, Stride: paletted.Stride, Rect: paletted.Rect, Palette: palette}
	}
	bounds := source.Bounds()
	result := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			result.Set(x, y, localRadarRainColor(source.At(x, y)))
		}
	}
	return result
}
