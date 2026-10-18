package cmd

import (
	"fmt"
	"image"
	"image/color"
	"net/http"
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

func darkRadarLayer(source image.Image, labels bool) image.Image {
	if source == nil {
		return nil
	}
	result := image.NewNRGBA(source.Bounds().Intersect(image.Rect(0, 0, localRadarSize, localRadarSize)))
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

func darkNationalRadarImage(source image.Image) image.Image {
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
