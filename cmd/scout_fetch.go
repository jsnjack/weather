package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HourlyForecast is a single hour of weather data for one location. WindDirection
// follows the meteorological convention: degrees the wind is coming FROM.
type HourlyForecast struct {
	Time          time.Time
	Temperature   float64 // °C
	Precipitation float64 // mm
	WindSpeed     float64 // km/h, sustained at 10m
	WindDirection float64 // degrees, 0 = from N
	WindGusts     float64 // km/h, max gust at 10m
}

type openMeteoRangeResponse struct {
	Elevation float64 `json:"elevation"` // metres; 0 for ocean cells in the model grid
	Hourly    struct {
		Time             []string  `json:"time"`
		Temperature2m    []float64 `json:"temperature_2m"`
		Precipitation    []float64 `json:"precipitation"`
		WindSpeed10m     []float64 `json:"wind_speed_10m"`
		WindDirection10m []float64 `json:"wind_direction_10m"`
		WindGusts10m     []float64 `json:"wind_gusts_10m"`
	} `json:"hourly"`
}

// OpenMeteoData is the parsed result of one Open-Meteo request: hourly weather
// plus the elevation of the weather-grid cell (used to detect sea points).
type OpenMeteoData struct {
	Hourly    []HourlyForecast
	Elevation float64
}

// IsSea returns true if the grid cell is ocean. Open-Meteo reports literal
// 0.0 for ocean grid cells in its weather model; below-sea-level polders and
// low coastal land return non-zero values. We match on exact 0 to avoid
// false-flagging Dutch polders as sea.
func (d *OpenMeteoData) IsSea() bool {
	return d.Elevation == 0.0
}

// GetOpenMeteoRange fetches hourly forecast data for a single lat/lon across
// the inclusive [startDate, endDate] range. Times come back in the local
// timezone of the point (timezone=auto) so hour-of-day comparisons are local.
// Retries up to three times on 429 (rate limit) — beam search can issue
// hundreds of calls.
func GetOpenMeteoRange(lat, lon float64, startDate, endDate time.Time) (*OpenMeteoData, error) {
	start := startDate.Format("2006-01-02")
	end := endDate.Format("2006-01-02")
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&hourly=temperature_2m,precipitation,wind_speed_10m,wind_direction_10m,wind_gusts_10m&timezone=auto&start_date=%s&end_date=%s",
		lat, lon, start, end,
	)
	DebugLogger.Printf("Requesting %s\n", url)

	client := &http.Client{Timeout: 15 * time.Second}

	var resp *http.Response
	for attempt := 0; attempt < 3; attempt++ {
		r, err := client.Get(url)
		if err != nil {
			return nil, err
		}
		if r.StatusCode != http.StatusTooManyRequests {
			resp = r
			break
		}
		r.Body.Close()
		DebugLogger.Printf("open-meteo 429 on %.4f,%.4f (attempt %d), backing off\n", lat, lon, attempt+1)
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}
	if resp == nil {
		return nil, fmt.Errorf("open-meteo kept returning 429 after retries")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("open-meteo status %d: %s", resp.StatusCode, string(body))
	}

	var parsed openMeteoRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	n := len(parsed.Hourly.Time)
	if len(parsed.Hourly.Temperature2m) != n ||
		len(parsed.Hourly.Precipitation) != n ||
		len(parsed.Hourly.WindSpeed10m) != n ||
		len(parsed.Hourly.WindDirection10m) != n ||
		len(parsed.Hourly.WindGusts10m) != n {
		return nil, fmt.Errorf("open-meteo returned inconsistent hourly array lengths")
	}

	hourly := make([]HourlyForecast, 0, n)
	for i, t := range parsed.Hourly.Time {
		// Open-Meteo returns times in the grid cell's local timezone
		// (timezone=auto). Parse them as local-time, not UTC, so instant
		// comparisons work for short-range planning within a single TZ.
		parsedTime, err := time.ParseInLocation("2006-01-02T15:04", t, time.Local)
		if err != nil {
			DebugLogger.Printf("scout: skipping unparseable time %q: %s\n", t, err)
			continue
		}
		hourly = append(hourly, HourlyForecast{
			Time:          parsedTime,
			Temperature:   parsed.Hourly.Temperature2m[i],
			Precipitation: parsed.Hourly.Precipitation[i],
			WindSpeed:     parsed.Hourly.WindSpeed10m[i],
			WindDirection: parsed.Hourly.WindDirection10m[i],
			WindGusts:     parsed.Hourly.WindGusts10m[i],
		})
	}
	return &OpenMeteoData{Hourly: hourly, Elevation: parsed.Elevation}, nil
}
