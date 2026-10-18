package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The ride heatmap reads only rain and wind. Unix times and hours relative to
// now keep the URL independent of the location's zone, so the grid can be
// fetched before that zone is known. The window starts today at the earliest
// and ends at most 24 hours after today's last start.
const (
	todayForecastQuery = "&hourly=precipitation,wind_speed_10m,wind_direction_10m&timeformat=unixtime&past_hours=24&forecast_hours=48"
	todayForecastURL   = "https://api.open-meteo.com/v1/forecast?latitude=%s&longitude=%s" + todayForecastQuery
)

// Open-Meteo counts every location as one call against its free limit of 600
// a minute, so a 21×21 grid costs 441. Grids are kept three times longer than
// other hourly forecasts; the model runs behind them update hourly at most.
const todayForecastTTL = 30 * time.Minute

// A grid normally loads in about a second. Web views stop waiting after this,
// so a stalled upstream shows a retry note instead of a page that hangs.
const todayForecastWait = 5 * time.Second

// Open-Meteo answers a few hundred locations in well under a second, but its
// proxy rejects URLs much past 8 KB (HTTP 414) — about 450 coordinate pairs.
const todayForecastBatchSize = 200

// todayForecastBatches splits n cells into equal batches of at most
// todayForecastBatchSize.
func todayForecastBatches(n int) [][2]int {
	count := (n + todayForecastBatchSize - 1) / todayForecastBatchSize
	out := make([][2]int, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, [2]int{i * n / count, (i + 1) * n / count})
	}
	return out
}

type todayForecastResponse struct {
	Elevation float64 `json:"elevation"`
	Hourly    struct {
		Time          []int64   `json:"time"`
		Precipitation []float64 `json:"precipitation"`
		WindSpeed     []float64 `json:"wind_speed_10m"`
		WindDirection []float64 `json:"wind_direction_10m"`
	} `json:"hourly"`
}

func getTodayForecastBatch(points []latLon) ([]*OpenMeteoData, error) {
	lats, lons := make([]string, len(points)), make([]string, len(points))
	for i, point := range points {
		lats[i], lons[i] = fmt.Sprintf("%.4f", point.Lat), fmt.Sprintf("%.4f", point.Lon)
	}
	body, err := openMeteoGetBody(fmt.Sprintf(todayForecastURL, strings.Join(lats, ","), strings.Join(lons, ",")))
	if err != nil {
		return nil, err
	}
	var entries []todayForecastResponse
	// A single location comes back as an object rather than a one-item array.
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '{' {
		entries = make([]todayForecastResponse, 1)
		err = json.Unmarshal(trimmed, &entries[0])
	} else {
		err = json.Unmarshal(body, &entries)
	}
	if err != nil {
		return nil, fmt.Errorf("decode ride forecast batch: %w", err)
	}
	if len(entries) != len(points) {
		return nil, fmt.Errorf("ride forecast returned %d locations, want %d", len(entries), len(points))
	}
	data := make([]*OpenMeteoData, len(entries))
	for i, entry := range entries {
		hours := entry.Hourly
		n := len(hours.Time)
		if len(hours.Precipitation) != n || len(hours.WindSpeed) != n || len(hours.WindDirection) != n {
			return nil, fmt.Errorf("ride forecast location %d has inconsistent hourly array lengths", i)
		}
		hourly := make([]HourlyForecast, n)
		for j, at := range hours.Time {
			hourly[j] = HourlyForecast{
				Time:          time.Unix(at, 0).UTC(),
				Precipitation: hours.Precipitation[j],
				WindSpeed:     hours.WindSpeed[j],
				WindDirection: hours.WindDirection[j],
			}
		}
		data[i] = &OpenMeteoData{Hourly: hourly, Elevation: entry.Elevation}
	}
	return data, nil
}
