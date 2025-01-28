package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// BuineradarResponse example https://graphdata.buienradar.nl/3.0/forecast/geo/RainHistoryForecast?lat=52.36&lon=4.92
type BuineradarResponse struct {
	Unit      string          `json:"unit"`
	Forecasts []RadarForecast `json:"forecasts"`
}

type RadarForecast struct {
	DateTime        string  `json:"dateTime"`
	UtcDateTime     string  `json:"utcDateTime"`
	DataValue       float64 `json:"dataValue"`
	PercentageValue float64 `json:"percentageValue"`
	Color           string  `json:"color"`
}

func GetBuineradarForecast(lat, long float64) (*Forecast, error) {
	DebugLogger.Printf("Getting Buineradar forecast for lat %.3f, long %.3f\n", lat, long)
	url := fmt.Sprintf("https://graphdata.buienradar.nl/3.0/forecast/geo/RainHistoryForecast?lat=%.3f&lon=%.3f", lat, long)
	DebugLogger.Printf("Requesting %s\n", url)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("forecast not available for this location")
		}
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	var buineradarResponse BuineradarResponse
	if err := json.NewDecoder(resp.Body).Decode(&buineradarResponse); err != nil {
		return nil, err
	}

	forecast := &Forecast{}
	for _, data := range buineradarResponse.Forecasts {
		t, err := time.Parse("2006-01-02T15:04:05", data.UtcDateTime)
		if err != nil {
			return nil, err
		}
		if t.After(time.Now()) {
			forecast.Data = append(forecast.Data, &ForecasePoint{
				Time:          t,
				Precipitation: data.DataValue,
			})
		}
	}
	return forecast, nil
}
