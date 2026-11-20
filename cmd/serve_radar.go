package cmd

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// KNMI publishes a ready-composited national radar loop — precipitation +
// lightning + temperature bubbles + time label, all baked into one animated GIF.
// It is NL-only and fixed-frame (can't centre on the user), which matches the
// rain view's existing NL-optimised, national-overview scope. We relay it,
// cropped to centre the Netherlands (centreKNMIRadarMap), rather than
// reproducing the map ourselves; see AGENTS.md for why the raw KNMI Data
// Platform (HDF5/NetCDF, key-gated) wasn't worth it.
const knmiRadarMapURL = "https://cdn.knmi.nl/knmi/map/general/weather-map.gif"

// radarMapCache holds the last-fetched GIF bytes process-wide for a short TTL,
// so a burst of page loads doesn't hammer KNMI's CDN. The map refreshes every
// few minutes upstream; 3 min keeps it current without re-fetching per client.
// The cached []byte is shared — treat it as read-only.
var radarMapCache = newTTLCache[[]byte](3*time.Minute, 1)
var darkRadarMapCache = newTTLCache[[]byte](3*time.Minute, 1)

// getKNMIRadarMap returns the KNMI national radar GIF, cached process-wide.
func getKNMIRadarMap() ([]byte, error) {
	return memo(radarMapCache, "knmi", func() ([]byte, error) {
		slog.Debug("knmi radar: fetching map", "url", knmiRadarMapURL)
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest("GET", knmiRadarMapURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer closeBody(resp.Body, "knmi radar response body")
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
		}
		const maxRadarBytes = 8 << 20
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxRadarBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read KNMI radar GIF: %w", err)
		}
		if len(body) > maxRadarBytes {
			return nil, fmt.Errorf("KNMI radar GIF exceeds size limit")
		}
		centred, err := centreKNMIRadarMap(body)
		if err != nil {
			slog.Warn("knmi radar: showing the uncropped map", "err", err)
			return body, nil
		}
		return centred, nil
	})
}

// KNMI's 755×815 map places the Netherlands in its upper right, beside open
// sea. Cropping to knmiRadarCrop centres the country (x 200–705, y 40–725 in
// the full map) with the same margin on every side. The time label, which the
// crop would cut off, moves with it to keep its place in the top-left corner.
var (
	knmiRadarFullSize   = image.Pt(755, 815)
	knmiRadarCrop       = image.Rect(150, 0, 755, 765)
	knmiRadarCropBounds = image.Rectangle{Max: knmiRadarCrop.Size()}
	knmiRadarLabel      = image.Rect(25, 20, 139, 74)
)

// The map's fills, border lines and time label, as KNMI draws them.
var (
	knmiRadarSea       = color.RGBA{220, 225, 235, 255}
	knmiRadarLand      = color.RGBA{185, 199, 214, 255}
	knmiRadarBorder    = color.RGBA{73, 101, 131, 255}
	knmiRadarLabelFill = color.RGBA{229, 241, 254, 255}
	knmiRadarLabelText = color.RGBA{55, 69, 83, 255}
)

func radarChroma(c color.RGBA) int {
	return int(max(c.R, c.G, c.B)) - int(min(c.R, c.G, c.B))
}

func radarLuminance(c color.RGBA) float64 {
	return .2126*float64(c.R) + .7152*float64(c.G) + .0722*float64(c.B)
}

// knmiRadarStations are the centres, in the cropped map, of the circles in
// which KNMI prints the temperature at its main stations: De Kooy, Eelde,
// Twenthe, De Bilt, Vlissingen and Maastricht. KNMI draws them only on observed
// frames. Each circle is a darkened disc with an outline about 32 px from its
// centre and white digits.
var knmiRadarStations = []image.Point{{220, 227}, {462, 155}, {526, 349}, {292, 417}, {80, 590}, {402, 691}}

const knmiRadarStationRadius = 31

// knmiRadarTemperatureShown reports whether a station's circle holds a
// temperature. Its white digits are the only light, near-neutral pixels on the
// map, because sea, land, borders and rain are all tinted.
func knmiRadarTemperatureShown(frame *image.RGBA, station image.Point) bool {
	const radius, minDigitPixels = 24, 20
	count := 0
	for y := station.Y - radius; y <= station.Y+radius; y++ {
		for x := station.X - radius; x <= station.X+radius; x++ {
			if !inRadarCircle(image.Pt(x, y), station, radius) || !image.Pt(x, y).In(frame.Bounds()) {
				continue
			}
			if c := frame.RGBAAt(x, y); radarChroma(c) <= 10 && radarLuminance(c) >= 180 {
				count++
			}
		}
	}
	return count >= minDigitPixels
}

func inRadarCircle(p, centre image.Point, radius int) bool {
	d := p.Sub(centre)
	return d.X*d.X+d.Y*d.Y <= radius*radius
}

// knmiRadarTemperatures returns the stations whose circles hold a temperature.
func knmiRadarTemperatures(frame *image.RGBA) []image.Point {
	var shown []image.Point
	for _, station := range knmiRadarStations {
		if knmiRadarTemperatureShown(frame, station) {
			shown = append(shown, station)
		}
	}
	return shown
}

// centreKNMIRadarMap crops every frame of KNMI's 755×815 map to the Netherlands
// and returns any other layout unchanged. Output frames are complete pictures
// with the source frame's palette, so delays and colours are kept.
func centreKNMIRadarMap(body []byte) ([]byte, error) {
	config, err := gif.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("read radar GIF dimensions: %w", err)
	}
	if image.Pt(config.Width, config.Height) != knmiRadarFullSize {
		return body, nil
	}
	animation, err := gif.DecodeAll(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decode radar GIF: %w", err)
	}
	size := knmiRadarCrop.Size()
	centred := &gif.GIF{
		Image:           make([]*image.Paletted, len(animation.Image)),
		Delay:           animation.Delay,
		Disposal:        make([]byte, len(animation.Image)),
		LoopCount:       animation.LoopCount,
		BackgroundIndex: animation.BackgroundIndex,
		Config:          image.Config{ColorModel: animation.Config.ColorModel, Width: size.X, Height: size.Y},
	}
	composeGIFFrames(animation, config, func(i int, canvas *image.RGBA) {
		palette := animation.Image[i].Palette
		frame := image.NewPaletted(image.Rect(0, 0, size.X, size.Y), palette)
		indices := map[color.RGBA]uint8{}
		for y := range size.Y {
			for x := range size.X {
				source := image.Pt(x, y)
				if !source.In(knmiRadarLabel) {
					source = source.Add(knmiRadarCrop.Min)
				}
				c := canvas.RGBAAt(source.X, source.Y)
				index, ok := indices[c]
				if !ok {
					index = uint8(palette.Index(c))
					indices[c] = index
				}
				frame.Pix[frame.PixOffset(x, y)] = index
			}
		}
		centred.Image[i] = frame
		centred.Disposal[i] = gif.DisposalNone
	})
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, centred); err != nil {
		return nil, fmt.Errorf("encode radar GIF: %w", err)
	}
	return encoded.Bytes(), nil
}

// handleRadarMap relays the KNMI radar GIF. It is location-independent (a fixed
// national frame), so there's no lat/lon query to thread through. A short
// browser cache mirrors the process cache; on upstream failure the rain page's
// line chart still carries the view, so a 502 here is non-fatal to the page.
func handleRadarMap(w http.ResponseWriter, r *http.Request) {
	dark, err := darkRadarRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	gif, err := getKNMIRadarMapTheme(dark)
	if err != nil {
		slog.Debug("knmi radar: fetch failed", "err", err)
		http.Error(w, "radar map unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "public, max-age=180")
	if _, werr := w.Write(gif); werr != nil {
		slog.Log(r.Context(), LevelTrace, "write radar map", "err", werr)
	}
}

func getKNMIRadarMapTheme(dark bool) ([]byte, error) {
	body, err := getKNMIRadarMap()
	if err != nil || !dark {
		return body, err
	}
	id := fmt.Sprintf("%x", sha256.Sum256(body))
	return memo(darkRadarMapCache, id, func() ([]byte, error) {
		animation, err := gif.DecodeAll(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("decode dark radar GIF: %w", err)
		}
		if image.Pt(animation.Config.Width, animation.Config.Height) == knmiRadarCrop.Size() {
			dark, err := darkKNMIRadarGIF(animation)
			if err != nil {
				slog.Warn("knmi radar: showing the light map in the dark theme", "err", err)
				return body, nil
			}
			return dark, nil
		}
		if animation.Config.Width != nationalRadarWidth || animation.Config.Height != nationalRadarHeight {
			return body, nil
		}
		convertPalette := func(source color.Palette) color.Palette {
			result := make(color.Palette, len(source))
			for i, c := range source {
				result[i] = darkNationalRadarColor(c)
			}
			return result
		}
		if palette, ok := animation.Config.ColorModel.(color.Palette); ok {
			animation.Config.ColorModel = convertPalette(palette)
		}
		for i, source := range animation.Image {
			frame := image.NewPaletted(source.Bounds(), convertPalette(source.Palette))
			copy(frame.Pix, source.Pix)
			for y := source.Bounds().Min.Y; y < source.Bounds().Max.Y; y++ {
				for x := source.Bounds().Min.X; x < source.Bounds().Max.X; x++ {
					if nationalRadarCaption(x, y, source.At(x, y)) {
						frame.Set(x, y, darkRadarText)
					}
				}
			}
			animation.Image[i] = frame
		}
		var encoded bytes.Buffer
		if err := gif.EncodeAll(&encoded, animation); err != nil {
			return nil, fmt.Errorf("encode dark radar GIF: %w", err)
		}
		return encoded.Bytes(), nil
	})
}
