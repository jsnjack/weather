package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"log/slog"
	"math"
	"net/http"
	"time"
)

const radarAtlasMaxPixels = 12_000_000

type radarTimeline struct {
	Width             int    `json:"width"`
	Height            int    `json:"height"`
	Columns           int    `json:"columns"`
	Delays            []int  `json:"delays"`
	AtlasURL          string `json:"atlasURL"`
	LatestObservation *int   `json:"latestObservation"`
	atlas             []byte
}

var radarTimelineCache = newTTLCache[*radarTimeline](6*time.Minute, 2)

func getRadarTimeline() (*radarTimeline, error) {
	body, err := getKNMIRadarMap()
	if err != nil {
		return nil, fmt.Errorf("fetch radar timeline: %w", err)
	}
	id := fmt.Sprintf("%x", sha256.Sum256(body))
	return memo(radarTimelineCache, id, func() (*radarTimeline, error) {
		timeline, err := decodeRadarTimeline(body)
		if err != nil {
			return nil, err
		}
		timeline.AtlasURL = "/radar/atlas.png?id=" + id
		return timeline, nil
	})
}

func decodeRadarTimeline(body []byte) (*radarTimeline, error) {
	config, err := gif.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("read radar GIF dimensions: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 2048 || config.Height > 2048 {
		return nil, fmt.Errorf("unsupported radar GIF dimensions")
	}
	animation, err := gif.DecodeAll(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decode radar GIF: %w", err)
	}
	count := len(animation.Image)
	if count == 0 {
		return nil, fmt.Errorf("radar animation has no frames")
	}
	columns := int(math.Ceil(math.Sqrt(float64(count))))
	rows := (count + columns - 1) / columns
	if config.Width*config.Height*columns*rows > radarAtlasMaxPixels {
		return nil, fmt.Errorf("radar animation exceeds frame budget")
	}
	canvas := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	atlas := image.NewRGBA(image.Rect(0, 0, columns*config.Width, rows*config.Height))
	background := color.Color(color.Transparent)
	if palette, ok := animation.Config.ColorModel.(color.Palette); ok && int(animation.BackgroundIndex) < len(palette) {
		background = palette[animation.BackgroundIndex]
	}
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
	timeline := &radarTimeline{Width: config.Width, Height: config.Height, Columns: columns, Delays: make([]int, count)}
	observations := make([]radarFrameKind, count)
	for i, frame := range animation.Image {
		disposal := byte(0)
		if len(animation.Disposal) > i {
			disposal = animation.Disposal[i]
		}
		var saved *image.RGBA
		if disposal == gif.DisposalPrevious {
			saved = image.NewRGBA(canvas.Bounds())
			draw.Draw(saved, saved.Bounds(), canvas, image.Point{}, draw.Src)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		observations[i] = radarObservationKind(canvas)
		x, y := (i%columns)*config.Width, (i/columns)*config.Height
		draw.Draw(atlas, image.Rect(x, y, x+config.Width, y+config.Height), canvas, image.Point{}, draw.Src)
		timeline.Delays[i] = max(100, animation.Delay[i]*10)
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			draw.Draw(canvas, canvas.Bounds(), saved, image.Point{}, draw.Src)
		}
	}
	timeline.LatestObservation = latestRadarObservation(observations)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, atlas); err != nil {
		return nil, fmt.Errorf("encode radar frames: %w", err)
	}
	timeline.atlas = encoded.Bytes()
	return timeline, nil
}

type radarFrameKind int

const (
	radarUnknown radarFrameKind = iota
	radarObserved
	radarPredicted
)

// KNMI's 425×445 map prints observed timestamps in black and forecast timestamps
// in red. This is a visual convention, not metadata; unknown layouts stay unknown.
func radarObservationKind(frame *image.RGBA) radarFrameKind {
	if frame.Bounds() != image.Rect(0, 0, 425, 445) {
		return radarUnknown
	}
	black, red := 0, 0
	for y := 429; y < 445; y++ {
		for x := 250; x < 425; x++ {
			pixel := frame.RGBAAt(x, y)
			if pixel.R < 60 && pixel.G < 60 && pixel.B < 60 {
				black++
			}
			if pixel.R >= 160 && pixel.G < 100 && pixel.B < 100 {
				red++
			}
		}
	}
	switch {
	case black >= 100 && red < 20:
		return radarObserved
	case red >= 100 && black < 20:
		return radarPredicted
	default:
		return radarUnknown
	}
}

func latestRadarObservation(frames []radarFrameKind) *int {
	latest := -1
	predicted := false
	for i, kind := range frames {
		switch kind {
		case radarObserved:
			if predicted {
				return nil
			}
			latest = i
		case radarPredicted:
			predicted = true
		default:
			return nil
		}
	}
	if latest < 0 {
		return nil
	}
	return &latest
}

func handleRadarTimeline(w http.ResponseWriter, r *http.Request) {
	timeline, err := getRadarTimeline()
	if err != nil {
		slog.Warn("load radar timeline", "err", err)
		http.Error(w, "radar timeline unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(timeline); err != nil {
		slog.Log(r.Context(), LevelTrace, "write radar timeline", "err", err)
	}
}

func handleRadarAtlas(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != sha256.Size {
		http.Error(w, "invalid radar snapshot", http.StatusBadRequest)
		return
	}
	timeline, ok := radarTimelineCache.get(id)
	if !ok {
		http.Error(w, "radar snapshot expired; reload the page", http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if _, err := w.Write(timeline.atlas); err != nil {
		slog.Log(r.Context(), LevelTrace, "write radar frames", "err", err)
	}
}
