package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

const openMeteoHourlyURL = "https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&hourly=temperature_2m,apparent_temperature,precipitation,precipitation_probability,wind_speed_10m,wind_direction_10m,wind_gusts_10m,uv_index,weather_code&daily=sunrise,sunset&timezone=auto"

// HourlyForecast is a single hour of weather data for one location. WindDirection
// follows the meteorological convention: degrees the wind is coming FROM.
type HourlyForecast struct {
	Time                     time.Time
	Temperature              float64 // °C
	ApparentTemperature      float64 // °C "feels like"
	Precipitation            float64 // mm
	PrecipitationProbability int     // 0-100 (% chance), 0 if unavailable
	WindSpeed                float64 // km/h, sustained at 10m
	WindDirection            float64 // degrees, 0 = from N
	WindGusts                float64 // km/h, max gust at 10m
	UVIndex                  float64 // 0-11+
	WeatherCode              int     // WMO weather interpretation code
}

type openMeteoRangeResponse struct {
	Elevation float64 `json:"elevation"` // metres; 0 for ocean cells in the model grid
	Timezone  string  `json:"timezone"`  // IANA zone of the wall-clock strings (timezone=auto)
	UTCOffset int     `json:"utc_offset_seconds"`
	Hourly    struct {
		Time             []string  `json:"time"`
		Temperature2m    []float64 `json:"temperature_2m"`
		ApparentTemp     []float64 `json:"apparent_temperature"`
		Precipitation    []float64 `json:"precipitation"`
		PrecipProb       []*int    `json:"precipitation_probability"` // null for past hours
		WindSpeed10m     []float64 `json:"wind_speed_10m"`
		WindDirection10m []float64 `json:"wind_direction_10m"`
		WindGusts10m     []float64 `json:"wind_gusts_10m"`
		UvIndex          []float64 `json:"uv_index"`
		WeatherCode      []int     `json:"weather_code"`
	} `json:"hourly"`
	Daily struct {
		Time    []string `json:"time"`
		Sunrise []string `json:"sunrise"`
		Sunset  []string `json:"sunset"`
	} `json:"daily"`
}

// OpenMeteoData is the parsed result of one Open-Meteo request: hourly weather
// plus the elevation of the weather-grid cell (used to detect sea points).
type OpenMeteoData struct {
	Hourly    []HourlyForecast
	Daily     []DailyForecast // sunrise/sunset per day, in local time
	Elevation float64
}

// DailyForecast carries the per-day sunrise and sunset (local zone).
type DailyForecast struct {
	Date    time.Time
	Sunrise time.Time
	Sunset  time.Time
}

// IsSea returns true if the grid cell is water. Empirically Open-Meteo's
// model reports:
//   - exactly 0.0 m for ocean, the IJsselmeer / Markermeer, and other open
//     water (its grid clamps surface water to NAP zero);
//   - -3 to -5 m for the deepest Dutch polders (Flevoland, Wieringermeer);
//   - +11 m for coastal cells averaged with dunes (Amsterdam, Schiphol).
//
// So matching on exact 0 correctly flags real water while keeping polders
// on the land side. Originally we relaxed this to <= -3 thinking polders
// would otherwise be miscategorised as sea — they aren't, because polders
// return *negative* values, not zero.
func (d *OpenMeteoData) IsSea() bool {
	return d.Elevation == 0.0
}

// GetOpenMeteoRange fetches hourly forecast data for a single lat/lon across
// the inclusive [startDate, endDate] range. Times come back in the local
// timezone of the point (timezone=auto) so hour-of-day comparisons are local.
// Retries up to four times with exponential back-off on any transient
// failure — rate limits, 5xx, or network errors. Beam search and the today
// heatmap issue 100+ parallel calls, where a single brief glitch otherwise
// shows up as a contiguous block of "no data" cells.
//
// Responses are cached in memory and on disk for ten minutes.
func GetOpenMeteoRange(lat, lon float64, startDate, endDate time.Time) (*OpenMeteoData, error) {
	return getOpenMeteoRangeUncached(lat, lon, startDate, endDate)
}

func getOpenMeteoRangeUncached(lat, lon float64, startDate, endDate time.Time) (*OpenMeteoData, error) {
	start := startDate.Format("2006-01-02")
	end := endDate.Format("2006-01-02")
	url := fmt.Sprintf(openMeteoHourlyURL+"&start_date=%s&end_date=%s", lat, lon, start, end)
	body, err := openMeteoGetBody(url)
	if err != nil {
		return nil, err
	}
	return parseOpenMeteoRange(body, lat, lon)
}

func parseOpenMeteoRange(body []byte, lat, lon float64) (*OpenMeteoData, error) {
	var parsed openMeteoRangeResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode open-meteo hourly forecast: %w", err)
	}

	n := len(parsed.Hourly.Time)
	if len(parsed.Hourly.Temperature2m) != n ||
		len(parsed.Hourly.ApparentTemp) != n ||
		len(parsed.Hourly.Precipitation) != n ||
		len(parsed.Hourly.PrecipProb) != n ||
		len(parsed.Hourly.WindSpeed10m) != n ||
		len(parsed.Hourly.WindDirection10m) != n ||
		len(parsed.Hourly.WindGusts10m) != n ||
		len(parsed.Hourly.UvIndex) != n ||
		len(parsed.Hourly.WeatherCode) != n {
		return nil, fmt.Errorf("open-meteo returned inconsistent hourly array lengths")
	}

	// Open-Meteo returns wall-clock strings in the grid cell's own timezone
	// (timezone=auto) and names that zone in the response. Parse with it so
	// every time is a correct instant regardless of the server's zone — a
	// UTC server once shifted Amsterdam's sunset by two hours everywhere.
	zone := openMeteoZone(parsed.Timezone, parsed.UTCOffset)
	rememberZone(lat, lon, zone)

	hourly := make([]HourlyForecast, 0, n)
	for i, t := range parsed.Hourly.Time {
		parsedTime, err := time.ParseInLocation("2006-01-02T15:04", t, zone)
		if err != nil {
			slog.Debug("open-meteo: skipping unparseable time", "time", t, "err", err)
			continue
		}
		// The repeated autumn hour is ambiguous without the preceding instant.
		if len(hourly) > 0 {
			next := hourly[len(hourly)-1].Time.Add(time.Hour)
			if next.Format("2006-01-02T15:04") == t {
				parsedTime = next
			}
		}
		prob := 0
		if parsed.Hourly.PrecipProb[i] != nil {
			prob = *parsed.Hourly.PrecipProb[i]
		}
		hourly = append(hourly, HourlyForecast{
			Time:                     parsedTime,
			Temperature:              parsed.Hourly.Temperature2m[i],
			ApparentTemperature:      parsed.Hourly.ApparentTemp[i],
			Precipitation:            parsed.Hourly.Precipitation[i],
			PrecipitationProbability: prob,
			WindSpeed:                parsed.Hourly.WindSpeed10m[i],
			WindDirection:            parsed.Hourly.WindDirection10m[i],
			WindGusts:                parsed.Hourly.WindGusts10m[i],
			UVIndex:                  parsed.Hourly.UvIndex[i],
			WeatherCode:              parsed.Hourly.WeatherCode[i],
		})
	}
	daily := make([]DailyForecast, 0, len(parsed.Daily.Time))
	if len(parsed.Daily.Time) == len(parsed.Daily.Sunrise) && len(parsed.Daily.Time) == len(parsed.Daily.Sunset) {
		for i, ds := range parsed.Daily.Time {
			d, dErr := time.ParseInLocation("2006-01-02", ds, zone)
			sr, srErr := time.ParseInLocation("2006-01-02T15:04", parsed.Daily.Sunrise[i], zone)
			ss, ssErr := time.ParseInLocation("2006-01-02T15:04", parsed.Daily.Sunset[i], zone)
			if dErr != nil || srErr != nil || ssErr != nil {
				continue
			}
			daily = append(daily, DailyForecast{Date: d, Sunrise: sr, Sunset: ss})
		}
	}
	return &OpenMeteoData{Hourly: hourly, Daily: daily, Elevation: parsed.Elevation}, nil
}

// openMeteoZone resolves the timezone Open-Meteo used for a response's
// wall-clock strings. Falls back to a fixed zone built from the reported
// offset when the IANA name can't be loaded, and to the process zone when
// the response carries no timezone at all.
func openMeteoZone(name string, offsetSec int) *time.Location {
	if name == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(name); err == nil {
		return loc
	}
	return time.FixedZone(name, offsetSec)
}
