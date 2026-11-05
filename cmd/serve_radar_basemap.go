package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"time"
)

const (
	radarBasemapTTL         = 7 * 24 * time.Hour
	radarBasemapTimeout     = 4 * time.Second
	radarTileSize           = 256
	radarWorldWidth         = 2 * math.Pi * radarEarthRadiusM
	radarBasemapMinFillZoom = 8
	pdokRadarTileURL        = "https://service.pdok.nl/kadaster/brt-achtergrondkaart/wmts/v2_0/%s/EPSG:3857/%02d/%d/%d.png"
)

var radarBasemapSlots = make(chan struct{}, 2)

var radarBasemapCache = newTTLCache[image.Image](radarBasemapTTL, 128)

// localRadarLayerCache holds composed map layers, which depend only on the
// map's geometry, so dry, rainy and unavailable maps share them.
var localRadarLayerCache = newTTLCache[image.Image](30*time.Minute, 32)
var radarBasemapGate struct {
	sync.Mutex
	cooldown time.Time
}

func radarBasemapTile(ctx context.Context, style string, zoom, col, row int) (image.Image, error) {
	requestURL := fmt.Sprintf(pdokRadarTileURL, style, zoom, col, row)
	return memo(radarBasemapCache, requestURL, func() (image.Image, error) {
		cachePath, cacheErr := forecastStorePath("pdok-radar-v1:" + requestURL)
		if cacheErr != nil {
			slog.Debug("basemap cache unavailable", "err", cacheErr)
		}
		if cacheErr == nil {
			if entry, ok := readStoredForecast(cachePath, time.Now()); ok {
				var body []byte
				if err := json.Unmarshal(entry.Body, &body); err == nil {
					if tile, err := decodeRadarTile(body); err == nil {
						return tile, nil
					}
				}
			}
		}
		radarBasemapGate.Lock()
		coolingDown := time.Now().Before(radarBasemapGate.cooldown)
		radarBasemapGate.Unlock()
		if coolingDown {
			return nil, fmt.Errorf("basemap requests are cooling down")
		}
		select {
		case radarBasemapSlots <- struct{}{}:
			defer func() { <-radarBasemapSlots }()
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for radar basemap: %w", ctx.Err())
		}
		radarBasemapGate.Lock()
		coolingDown = time.Now().Before(radarBasemapGate.cooldown)
		radarBasemapGate.Unlock()
		if coolingDown {
			return nil, fmt.Errorf("basemap requests are cooling down")
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("load radar basemap: %w", err)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create basemap request: %w", err)
		}
		client := http.Client{Timeout: 3 * time.Second}
		response, err := client.Do(request)
		if err != nil {
			radarBasemapGate.Lock()
			radarBasemapGate.cooldown = time.Now().Add(30 * time.Second)
			radarBasemapGate.Unlock()
			return nil, fmt.Errorf("fetch radar basemap: %w", err)
		}
		defer closeBody(response.Body, "PDOK map tile")
		if response.StatusCode != http.StatusOK {
			radarBasemapGate.Lock()
			radarBasemapGate.cooldown = retryAfter(response.Header.Get("Retry-After"), time.Now())
			radarBasemapGate.Unlock()
			return nil, fmt.Errorf("radar basemap HTTP %d", response.StatusCode)
		}
		const maxBytes = 1 << 20
		body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read radar basemap: %w", err)
		}
		if len(body) > maxBytes {
			return nil, fmt.Errorf("basemap tile exceeds size limit")
		}
		tile, err := decodeRadarTile(body)
		if err != nil {
			return nil, err
		}
		if cacheErr == nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("encode basemap cache: %w", err)
			}
			if err := writeStoredForecast(cachePath, storedForecast{Body: encoded, Expires: time.Now().Add(radarBasemapTTL)}); err != nil {
				slog.Debug("save radar basemap cache", "err", err)
			}
		}
		return tile, nil
	})
}

func decodeRadarTile(body []byte) (image.Image, error) {
	config, err := png.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("read radar basemap PNG: %w", err)
	}
	if config.Width != radarTileSize || config.Height != radarTileSize {
		return nil, fmt.Errorf("unexpected radar basemap tile size")
	}
	tile, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("decode radar basemap PNG: %w", err)
	}
	return tile, nil
}

// localRadarBasemapLayers loads the background and label layers within the
// basemap budget. A layer that fails is nil, so the map falls back to the
// embedded land outline; failures are not cached.
func localRadarBasemapLayers(ctx context.Context, radar *localRadarImage) (background, labels image.Image) {
	ctx, cancel := context.WithTimeout(ctx, radarBasemapTimeout)
	defer cancel()
	load := func(style string) image.Image {
		key := fmt.Sprintf("%s:%d:%.3f,%.3f,%.3f", style, radar.mapSize(), radar.x, radar.y, radar.halfWidth)
		layer, err := memo(localRadarLayerCache, key, func() (image.Image, error) {
			if layer := localRadarBasemap(ctx, radar, style); layer != nil {
				return layer, nil
			}
			return nil, fmt.Errorf("radar basemap %s is unavailable", style)
		})
		if err != nil {
			slog.Log(ctx, LevelTrace, "draw radar without basemap layer", "err", err)
		}
		return layer
	}
	var wg sync.WaitGroup
	wg.Go(func() { background = load("grijs") })
	wg.Go(func() { labels = load("labels") })
	wg.Wait()
	return background, labels
}

func localRadarBasemap(ctx context.Context, radar *localRadarImage, style string) image.Image {
	size := radar.mapSize()
	// Labels keep the default map's zoom, so larger maps enlarge the text
	// with the map instead of shrinking it relative to the map.
	zoomSize := size
	if style == "labels" {
		zoomSize = localRadarSize
	}
	// Match the native image resolution to keep town labels and roads legible.
	resolution := 2 * radar.halfWidth / float64(zoomSize)
	zoom := int(math.Round(math.Log2(radarWorldWidth / (radarTileSize * resolution))))
	zoom = max(0, min(zoom, 18))
	// Below zoom 8 PDOK leaves the sea transparent, so the 100 km map's
	// background comes from zoom 8 tiles, halved; labels keep their size.
	if style != "labels" {
		zoom = max(zoom, radarBasemapMinFillZoom)
	}
	tileCount := 1 << zoom
	pixelsPerMetre := float64(radarTileSize*tileCount) / radarWorldWidth
	left := (radar.x - radar.halfWidth + radarWorldWidth/2) * pixelsPerMetre
	top := (radarWorldWidth/2 - radar.y - radar.halfWidth) * pixelsPerMetre
	span := 2 * radar.halfWidth * pixelsPerMetre
	col0, row0 := int(math.Floor(left/radarTileSize)), int(math.Floor(top/radarTileSize))
	col1, row1 := int(math.Floor((left+span)/radarTileSize)), int(math.Floor((top+span)/radarTileSize))
	mosaic := image.NewRGBA(image.Rect(col0*radarTileSize, row0*radarTileSize, (col1+1)*radarTileSize, (row1+1)*radarTileSize))
	ctx, cancel := context.WithTimeout(ctx, radarBasemapTimeout)
	defer cancel()
	type tileResult struct {
		col, row int
		image    image.Image
		err      error
	}
	results := make([]tileResult, (row1-row0+1)*(col1-col0+1))
	var wg sync.WaitGroup
	for row := row0; row <= row1; row++ {
		for col := col0; col <= col1; col++ {
			i := (row-row0)*(col1-col0+1) + col - col0
			wg.Go(func() {
				tile, err := radarBasemapTile(ctx, style, zoom, col, row)
				results[i] = tileResult{col, row, tile, err}
			})
		}
	}
	wg.Wait()
	for _, tile := range results {
		if tile.err != nil {
			slog.Debug("use embedded radar basemap", "err", tile.err)
			return nil
		}
		rect := image.Rect(tile.col*radarTileSize, tile.row*radarTileSize, (tile.col+1)*radarTileSize, (tile.row+1)*radarTileSize)
		draw.Draw(mosaic, rect, tile.image, image.Point{}, draw.Src)
	}
	if size > zoomSize {
		return upscaleRadarLabels(mosaic, left, top, span, size)
	}
	return resampleRadarBasemap(mosaic, left, top, span, size)
}

func resampleRadarBasemap(source image.Image, left, top, span float64, size int) image.Image {
	result := image.NewRGBA(image.Rect(0, 0, size, size))
	bounds := source.Bounds()
	for y := range size {
		for x := range size {
			sx := left + (float64(x)+.5)*span/float64(size) - .5
			sy := top + (float64(y)+.5)*span/float64(size) - .5
			ix, iy := int(math.Floor(sx)), int(math.Floor(sy))
			dx, dy := sx-float64(ix), sy-float64(iy)
			var channels [4]float64
			for j := range 2 {
				for i := range 2 {
					weight := []float64{1 - dx, dx}[i] * []float64{1 - dy, dy}[j]
					r, g, b, a := source.At(max(bounds.Min.X, min(bounds.Max.X-1, ix+i)), max(bounds.Min.Y, min(bounds.Max.Y-1, iy+j))).RGBA()
					for c, v := range []uint32{r, g, b, a} {
						channels[c] += float64(v) / 257 * weight
					}
				}
			}
			result.SetRGBA(x, y, color.RGBA{uint8(math.Round(channels[0])), uint8(math.Round(channels[1])), uint8(math.Round(channels[2])), uint8(math.Round(channels[3]))})
		}
	}
	return result
}

// upscaleRadarLabels enlarges a label mosaic with a separable Catmull-Rom
// filter. It works on premultiplied colour, so transparent surroundings do
// not darken the text halos, and clamps the filter's overshoot.
func upscaleRadarLabels(source *image.RGBA, left, top, span float64, size int) *image.RGBA {
	type taps struct {
		index  [4]int
		weight [4]float64
	}
	sample := func(origin float64, lo, hi int) []taps {
		result := make([]taps, size)
		for i := range result {
			s := origin + (float64(i)+.5)*span/float64(size) - .5
			base := int(math.Floor(s))
			for k := range 4 {
				result[i].index[k] = max(lo, min(hi-1, base-1+k))
				result[i].weight[k] = catmullRom(s - float64(base-1+k))
			}
		}
		return result
	}
	bounds := source.Bounds()
	columns := sample(left, bounds.Min.X, bounds.Max.X)
	rows := sample(top, bounds.Min.Y, bounds.Max.Y)
	first, last := rows[0].index[0], rows[size-1].index[3]
	horizontal := make([]float64, (last-first+1)*size*4)
	for row := first; row <= last; row++ {
		for x, column := range columns {
			out := horizontal[((row-first)*size+x)*4:]
			for k, sx := range column.index {
				pixel := source.Pix[source.PixOffset(sx, row):]
				for c := range 4 {
					out[c] += column.weight[k] * float64(pixel[c])
				}
			}
		}
	}
	result := image.NewRGBA(image.Rect(0, 0, size, size))
	for y, row := range rows {
		for x := range size {
			var channels [4]float64
			for k, sy := range row.index {
				in := horizontal[((sy-first)*size+x)*4:]
				for c := range 4 {
					channels[c] += row.weight[k] * in[c]
				}
			}
			alpha := math.Round(max(0, min(255, channels[3])))
			pixel := result.Pix[result.PixOffset(x, y):]
			for c := range 3 {
				pixel[c] = uint8(math.Round(max(0, min(alpha, channels[c]))))
			}
			pixel[3] = uint8(alpha)
		}
	}
	return result
}

// catmullRom is the Catmull-Rom cubic kernel at distance d from a sample.
func catmullRom(d float64) float64 {
	d = math.Abs(d)
	switch {
	case d < 1:
		return 1.5*d*d*d - 2.5*d*d + 1
	case d < 2:
		return -.5*d*d*d + 2.5*d*d - 4*d + 2
	default:
		return 0
	}
}
