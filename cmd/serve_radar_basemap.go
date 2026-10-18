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
	radarBasemapTTL     = 7 * 24 * time.Hour
	radarBasemapTimeout = 4 * time.Second
	radarTileSize       = 256
	radarWorldWidth     = 2 * math.Pi * radarEarthRadiusM
	pdokRadarTileURL    = "https://service.pdok.nl/kadaster/brt-achtergrondkaart/wmts/v2_0/%s/EPSG:3857/%02d/%d/%d.png"
)

var radarBasemapSlots = make(chan struct{}, 2)

var radarBasemapCache = newTTLCache[image.Image](radarBasemapTTL, 128)
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

func localRadarBasemap(ctx context.Context, radar *localRadarImage, style string) image.Image {
	// Match the native image resolution to keep town labels and roads legible.
	resolution := 2 * radar.halfWidth / localRadarSize
	zoom := int(math.Round(math.Log2(radarWorldWidth / (radarTileSize * resolution))))
	zoom = max(0, min(zoom, 18))
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
	return resampleRadarBasemap(mosaic, left, top, span)
}

func resampleRadarBasemap(source image.Image, left, top, span float64) image.Image {
	result := image.NewRGBA(image.Rect(0, 0, localRadarSize, localRadarSize))
	bounds := source.Bounds()
	for y := range localRadarSize {
		for x := range localRadarSize {
			sx := left + (float64(x)+.5)*span/localRadarSize - .5
			sy := top + (float64(y)+.5)*span/localRadarSize - .5
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
