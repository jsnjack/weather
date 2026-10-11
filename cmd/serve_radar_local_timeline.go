package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

const (
	radarForecastDataset      = "radar_forecast_2.0"
	localRadarFrameDelay      = 750
	localRadarTimelineTimeout = 45 * time.Second
)

type localRadarTimeline struct {
	Width             int         `json:"width"`
	Height            int         `json:"height"`
	Columns           int         `json:"columns"`
	Delays            []int       `json:"delays"`
	Times             []time.Time `json:"times"`
	Reference         time.Time   `json:"reference"`
	AtlasURL          string      `json:"atlasURL"`
	LatestObservation int         `json:"latestObservation"`
	atlas             []byte
}

var radarForecastRunCache = newTTLCache[time.Time](3*time.Minute, 1)
var radarForecastFrameCache = newTTLCache[image.Image](3*time.Minute, 256)
var localRadarTimelineCache = newTTLCache[*localRadarTimeline](6*time.Minute, 16)

func localRadarForecastTimes(observed, reference time.Time) []time.Time {
	times := []time.Time{observed}
	for at := reference; !at.After(observed.Add(time.Hour)); at = at.Add(5 * time.Minute) {
		if at.After(observed) {
			times = append(times, at)
		}
	}
	return times
}

func localRadarForecastFrame(ctx context.Context, radar *localRadarImage, reference, at time.Time) (image.Image, error) {
	bbox := fmt.Sprintf("%.3f,%.3f,%.3f,%.3f", radar.x-radar.halfWidth, radar.y-radar.halfWidth, radar.x+radar.halfWidth, radar.y+radar.halfWidth)
	key := bbox + ":" + reference.Format(time.RFC3339) + ":" + at.Format(time.RFC3339)
	return memo(radarForecastFrameCache, key, func() (image.Image, error) {
		body, err := radarWMSRequest(ctx, url.Values{
			"DATASET": {radarForecastDataset}, "REQUEST": {"GetMap"}, "VERSION": {"1.3.0"},
			"LAYERS": {"precipitation_nowcast"}, "STYLES": {"radar/nearest"},
			"FORMAT": {"image/png"}, "TRANSPARENT": {"TRUE"}, "CRS": {"EPSG:3857"},
			"BBOX": {bbox}, "WIDTH": {"256"}, "HEIGHT": {"256"},
			"TIME": {at.Format(time.RFC3339)}, "DIM_REFERENCE_TIME": {reference.Format(time.RFC3339)},
		})
		if err != nil {
			return nil, err
		}
		overlay, err := png.Decode(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("decode radar forecast: %w", err)
		}
		return overlay, nil
	})
}

func getLocalRadarTimeline(ctx context.Context, lat, lon float64) (*localRadarTimeline, error) {
	ctx, cancel := context.WithTimeout(ctx, localRadarTimelineTimeout)
	defer cancel()
	radar, err := getLocalRadar(ctx, lat, lon)
	if err != nil {
		return nil, err
	}
	if radar == nil || !freshRadarObservation(radar.observed, time.Now()) || !hasLocalRadarRain(radar.overlay) {
		return nil, nil
	}
	reference, err := memo(radarForecastRunCache, "latest", func() (time.Time, error) {
		body, err := radarWMSRequest(ctx, url.Values{"DATASET": {radarForecastDataset}, "REQUEST": {"GetCapabilities"}})
		if err != nil {
			return time.Time{}, err
		}
		return parseRadarDimensionTime(body, "precipitation_nowcast", "reference_time")
	})
	if err != nil {
		return nil, err
	}
	if !freshRadarObservation(reference, time.Now()) || reference.After(radar.observed.Add(5*time.Minute)) {
		return nil, fmt.Errorf("radar forecast run is stale or ahead of the observation")
	}
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%.17g,%.17g,%s,%s", lat, lon, radar.observed.Format(time.RFC3339), reference.Format(time.RFC3339)))))
	return memo(localRadarTimelineCache, id, func() (*localRadarTimeline, error) {
		times := localRadarForecastTimes(radar.observed, reference)
		const columns = 4
		rows := (len(times) + columns - 1) / columns
		atlas := image.NewRGBA(image.Rect(0, 0, columns*localRadarSize, rows*localRadarSize))
		timeline := &localRadarTimeline{Width: localRadarSize, Height: localRadarSize, Columns: columns,
			Times: times, Reference: reference, Delays: make([]int, len(times)), AtlasURL: "/radar/local-atlas.png?id=" + id}
		for i, at := range times {
			frame := *radar
			if i > 0 {
				frame.overlay, err = localRadarForecastFrame(ctx, radar, reference, at)
				if err != nil {
					return nil, fmt.Errorf("load radar forecast at %s: %w", at.Format(time.RFC3339), err)
				}
			}
			picture, err := renderLocalRadarImage(&frame, lat, lon)
			if err != nil {
				return nil, err
			}
			x, y := i%columns*localRadarSize, i/columns*localRadarSize
			draw.Draw(atlas, image.Rect(x, y, x+localRadarSize, y+localRadarSize), picture, image.Point{}, draw.Src)
			timeline.Delays[i] = localRadarFrameDelay
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, atlas); err != nil {
			return nil, fmt.Errorf("encode local radar timeline: %w", err)
		}
		timeline.atlas = encoded.Bytes()
		return timeline, nil
	})
}

func handleLocalRadarTimeline(w http.ResponseWriter, r *http.Request) {
	loc, _, err := resolveRequestLocation(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	timeline, err := getLocalRadarTimeline(r.Context(), loc.Latitude, loc.Longitude)
	if err != nil {
		slog.Debug("local radar forecast unavailable", "err", err)
		http.Error(w, "local radar forecast unavailable", http.StatusBadGateway)
		return
	}
	if timeline == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(timeline); err != nil {
		slog.Log(r.Context(), LevelTrace, "write local radar timeline", "err", err)
	}
}

func handleLocalRadarAtlas(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if decoded, err := hex.DecodeString(id); err != nil || len(decoded) != sha256.Size {
		http.Error(w, "invalid radar snapshot", http.StatusBadRequest)
		return
	}
	timeline, ok := localRadarTimelineCache.get(id)
	if !ok {
		http.Error(w, "radar snapshot expired; reload the page", http.StatusGone)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=360")
	if _, err := w.Write(timeline.atlas); err != nil {
		slog.Log(r.Context(), LevelTrace, "write local radar frames", "err", err)
	}
}
