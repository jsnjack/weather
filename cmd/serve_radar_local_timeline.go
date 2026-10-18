package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	radarForecastInterval     = 5 * time.Minute
	localRadarFrameDelay      = 750
	localRadarTimelineTimeout = 45 * time.Second
	// Every second KNMI forecast keeps playback readable with half the WMS
	// requests.
	localRadarForecastStep = 10 * time.Minute
	// KNMI forecast runs end two hours after their reference time.
	radarForecastHorizon = 2 * time.Hour
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
var localRadarRenderedFrameCache = newTTLCache[[]byte](6*time.Minute, 256)

// localRadarForecastTimes counts KNMI's forecast steps after the observation
// and keeps every localRadarForecastStep, so frames sit at whole steps from
// the observation whenever the run allows it. Playback reaches two hours past
// the observation or the end of the run, whichever comes first.
func localRadarForecastTimes(observed, reference time.Time) []time.Time {
	times := []time.Time{observed}
	stride := int(localRadarForecastStep / radarForecastInterval)
	last := observed.Add(radarForecastHorizon)
	if end := reference.Add(radarForecastHorizon); end.Before(last) {
		last = end
	}
	steps := 0
	for at := reference; !at.After(last); at = at.Add(radarForecastInterval) {
		if at.After(observed) {
			steps++
			if steps%stride == 0 {
				times = append(times, at)
			}
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
	return getLocalRadarTimelineTheme(ctx, lat, lon, false)
}

func getLocalRadarTimelineTheme(ctx context.Context, lat, lon float64, dark bool) (*localRadarTimeline, error) {
	ctx, cancel := context.WithTimeout(ctx, localRadarTimelineTimeout)
	defer cancel()
	radar, timeline, id, err := prepareLocalRadarTimeline(ctx, lat, lon, dark)
	if err != nil || timeline == nil {
		return nil, err
	}
	return memo(localRadarTimelineCache, id, func() (*localRadarTimeline, error) {
		rows := (len(timeline.Times) + timeline.Columns - 1) / timeline.Columns
		atlas := image.NewRGBA(image.Rect(0, 0, timeline.Columns*localRadarSize, rows*localRadarSize))
		for i, frame := range loadLocalRadarTimelineFrames(ctx, radar, timeline, id, lat, lon, dark) {
			body, err := frame.wait(ctx)
			if err != nil {
				return nil, err
			}
			picture, err := png.Decode(bytes.NewReader(body))
			if err != nil {
				return nil, fmt.Errorf("decode rendered radar frame: %w", err)
			}
			x, y := i%timeline.Columns*localRadarSize, i/timeline.Columns*localRadarSize
			draw.Draw(atlas, image.Rect(x, y, x+localRadarSize, y+localRadarSize), picture, image.Point{}, draw.Src)
		}
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, atlas); err != nil {
			return nil, fmt.Errorf("encode local radar timeline: %w", err)
		}
		timeline.atlas = encoded.Bytes()
		return timeline, nil
	})
}

func prepareLocalRadarTimeline(ctx context.Context, lat, lon float64, dark bool) (*localRadarImage, *localRadarTimeline, string, error) {
	radar, err := getLocalRadar(ctx, lat, lon)
	if err != nil {
		return nil, nil, "", err
	}
	if radar == nil || !freshRadarObservation(radar.observed, time.Now()) || !hasLocalRadarRain(radar.overlay) {
		return nil, nil, "", nil
	}
	reference, err := memo(radarForecastRunCache, "latest", func() (time.Time, error) {
		body, err := radarWMSRequest(ctx, url.Values{"DATASET": {radarForecastDataset}, "REQUEST": {"GetCapabilities"}})
		if err != nil {
			return time.Time{}, err
		}
		return parseRadarDimensionTime(body, "precipitation_nowcast", "reference_time")
	})
	if err != nil {
		return nil, nil, "", err
	}
	if !freshRadarObservation(reference, time.Now()) || reference.After(radar.observed.Add(5*time.Minute)) {
		return nil, nil, "", fmt.Errorf("radar forecast run is stale or ahead of the observation")
	}
	id := fmt.Sprintf("%x", sha256.Sum256(fmt.Appendf(nil, "%.17g,%.17g,%s,%s,%t", lat, lon, radar.observed.Format(time.RFC3339), reference.Format(time.RFC3339), dark)))
	times := localRadarForecastTimes(radar.observed, reference)
	timeline := &localRadarTimeline{Width: localRadarSize, Height: localRadarSize, Columns: 4,
		Times: times, Reference: reference, Delays: make([]int, len(times)), AtlasURL: "/radar/local-atlas.png?id=" + id}
	for i := range times {
		timeline.Delays[i] = localRadarFrameDelay
	}
	return radar, timeline, id, nil
}

func localRadarTimelineFrame(ctx context.Context, radar *localRadarImage, timeline *localRadarTimeline, id string, index int, lat, lon float64, dark bool) ([]byte, error) {
	return memo(localRadarRenderedFrameCache, fmt.Sprintf("%s:%d", id, index), func() ([]byte, error) {
		frame := *radar
		if index > 0 {
			overlay, err := localRadarForecastFrame(ctx, radar, timeline.Reference, timeline.Times[index])
			if err != nil {
				return nil, fmt.Errorf("load radar forecast frame %d: %w", index, err)
			}
			frame.overlay = overlay
		}
		return renderLocalRadarTheme(&frame, lat, lon, dark)
	})
}

type localRadarFrameLoad struct {
	done chan struct{}
	body []byte
	err  error
}

func (f *localRadarFrameLoad) wait(ctx context.Context) ([]byte, error) {
	select {
	case <-f.done:
		return f.body, f.err
	case <-ctx.Done():
		return nil, fmt.Errorf("wait for radar frame: %w", ctx.Err())
	}
}

// loadLocalRadarTimelineFrames starts every frame at once. The WMS gate still
// paces upstream requests, but each frame no longer waits for the previous
// frame's round trip.
func loadLocalRadarTimelineFrames(ctx context.Context, radar *localRadarImage, timeline *localRadarTimeline, id string, lat, lon float64, dark bool) []*localRadarFrameLoad {
	frames := make([]*localRadarFrameLoad, len(timeline.Times))
	for i := range frames {
		frame := &localRadarFrameLoad{done: make(chan struct{})}
		frames[i] = frame
		go func() {
			defer close(frame.done)
			frame.body, frame.err = localRadarTimelineFrame(ctx, radar, timeline, id, i, lat, lon, dark)
		}()
	}
	return frames
}

func handleLocalRadarTimeline(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("progressive") == "1" {
		handleLocalRadarStream(w, r)
		return
	}
	dark, err := darkRadarRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	loc, _, err := resolveRequestLocation(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	timeline, err := getLocalRadarTimelineTheme(r.Context(), loc.Latitude, loc.Longitude, dark)
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

func handleLocalRadarStream(w http.ResponseWriter, r *http.Request) {
	dark, err := darkRadarRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	loc, _, err := resolveRequestLocation(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), localRadarTimelineTimeout)
	defer cancel()
	radar, timeline, id, err := prepareLocalRadarTimeline(ctx, loc.Latitude, loc.Longitude, dark)
	if err != nil {
		slog.Debug("local radar stream unavailable", "err", err)
		http.Error(w, "local radar forecast unavailable", http.StatusBadGateway)
		return
	}
	if timeline == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	controller := http.NewResponseController(w)
	send := func(event any) bool {
		if err := encoder.Encode(event); err != nil {
			slog.Log(ctx, LevelTrace, "write radar stream", "err", err)
			return false
		}
		if err := controller.Flush(); err != nil {
			slog.Log(ctx, LevelTrace, "flush radar stream", "err", err)
			return false
		}
		return true
	}
	if !send(struct {
		Type string `json:"type"`
		*localRadarTimeline
	}{"metadata", timeline}) {
		return
	}
	for i, frame := range loadLocalRadarTimelineFrames(ctx, radar, timeline, id, loc.Latitude, loc.Longitude, dark) {
		body, err := frame.wait(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Debug("local radar stream frame unavailable", "err", err)
			send(struct {
				Type string `json:"type"`
			}{"error"})
			return
		}
		if !send(struct {
			Type  string `json:"type"`
			Index int    `json:"index"`
			Image string `json:"image"`
		}{
			"frame", i, "data:image/png;base64," + base64.StdEncoding.EncodeToString(body),
		}) {
			return
		}
	}
	send(struct {
		Type string `json:"type"`
	}{"complete"})
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
