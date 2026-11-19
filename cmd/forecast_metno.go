package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// MET Norway's radar nowcast covers Norway, Sweden, Finland and Denmark in
// 5-minute steps up to about 1h50. Its terms ask for an identifying
// User-Agent, at most 4 decimals in coordinates and no request before the
// previous response's Expires time. CC BY 4.0.
type metnoNowcastResponse struct {
	Properties struct {
		Meta struct {
			// "ok" when radar covers the point; otherwise "no coverage" or
			// "temporarily unavailable".
			RadarCoverage string `json:"radar_coverage"`
		} `json:"meta"`
		Timeseries []struct {
			Time string `json:"time"`
			Data struct {
				Instant struct {
					Details struct {
						PrecipitationRate *float64 `json:"precipitation_rate"` // mm/h
					} `json:"details"`
				} `json:"instant"`
			} `json:"data"`
		} `json:"timeseries"`
	} `json:"properties"`
}

// GetMetNoForecast returns MET Norway's radar nowcast, cached for five
// minutes, which is at least as long as its Expires header.
func GetMetNoForecast(lat, lon float64) (*Forecast, error) {
	return memo(metnoCache, fmt.Sprintf("%.3f|%.3f", lat, lon), func() (*Forecast, error) {
		url := fmt.Sprintf("https://api.met.no/weatherapi/nowcast/2.0/complete?lat=%.4f&lon=%.4f", lat, lon)
		slog.Debug("metno: requesting", "url", url)
		body, err := nowcastGet(url, "metno", 10*time.Second)
		if err != nil {
			return nil, err
		}
		return parseMetNoNowcast(body, time.Now(), nowcastZone(lat, lon))
	})
}

func parseMetNoNowcast(body []byte, now time.Time, zone *time.Location) (*Forecast, error) {
	var response metnoNowcastResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode metno nowcast: %w", err)
	}
	if coverage := response.Properties.Meta.RadarCoverage; coverage != "ok" {
		return nil, fmt.Errorf("metno: radar coverage %q", coverage)
	}
	forecast := &Forecast{Desc: "MET Norway", Type: PrecipitationForecast, Source: "MET Norway"}
	for _, step := range response.Properties.Timeseries {
		t, err := time.Parse(time.RFC3339, step.Time)
		if err != nil {
			return nil, fmt.Errorf("metno time %q: %w", step.Time, err)
		}
		rate := step.Data.Instant.Details.PrecipitationRate
		if rate == nil || !t.After(now) {
			continue
		}
		forecast.Data = append(forecast.Data, ForecastDataPoint{Time: t.In(zone), Value: *rate})
	}
	return forecast, nil
}
