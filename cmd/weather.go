package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// BuinealarmResponse example https://cdn.buienalarm.nl/api/4.0/nowcast/timeseries/52.37/4.96
type BuinealarmResponse struct {
	Data           []PrecipitationData `json:"data"`
	NowcastMessage NowcastMessage      `json:"nowcastmessage"`
}

type PrecipitationData struct {
	PrecipitationRate float64 `json:"precipitationrate"`
	PrecipitationType string  `json:"precipitationtype"`
	Timestamp         int64   `json:"timestamp"`
	Time              string  `json:"time"`
}

type NowcastMessage struct {
	En string `json:"en"`
	De string `json:"de"`
	Nl string `json:"nl"`
}

type ForecasePoint struct {
	Time          time.Time
	Precipitation float64
}

type Forecast struct {
	Temperature int
	Data        []*ForecasePoint
	Desc        string
}

func (f *Forecast) RainString() string {
	// Determine the maximum precipitation level
	maxPrecipitation := 0.0
	for _, point := range f.Data {
		if point.Precipitation > maxPrecipitation {
			maxPrecipitation = point.Precipitation
		}
	}

	// Determine the rain level based on the maximum precipitation
	rainInfo := "No rain expected."
	rainingNow := f.Data[0].Precipitation > 0
	if maxPrecipitation > 0 {
		if maxPrecipitation <= 0.25 {
			rainInfo = "Light rain expected."
		} else if maxPrecipitation <= 1.0 {
			rainInfo = "Moderate rain expected."
		} else {
			rainInfo = "Heavy rain expected."
		}
		if !rainingNow {
			// When the next rain starts?
			for _, point := range f.Data {
				if point.Precipitation > 0 {
					rainInfo += fmt.Sprintf(" Next rain starts at %s.", point.Time.Format("15:04"))
					break
				}
			}
		}
	}
	return rainInfo
}

func GetForecast(lat, long float64) (*Forecast, error) {
	DebugLogger.Printf("Getting forecast for lat %.2f, long %.2f\n", lat, long)
	url := fmt.Sprintf("https://cdn.buienalarm.nl/api/4.0/nowcast/timeseries/%.2f/%.2f", lat, long)
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

	var buinealarmResponse BuinealarmResponse
	if err := json.NewDecoder(resp.Body).Decode(&buinealarmResponse); err != nil {
		return nil, err
	}

	forecast := &Forecast{}
	for _, data := range buinealarmResponse.Data {
		t := time.Unix(data.Timestamp, 0)
		// Filter out the data points from the past
		if t.After(time.Now()) {
			forecast.Data = append(forecast.Data, &ForecasePoint{
				Time:          t,
				Precipitation: data.PrecipitationRate,
			})
		}
	}
	forecast.Desc = buinealarmResponse.NowcastMessage.En
	timestampRe := regexp.MustCompile(`\{(\d+)\}`)

	// Replace function
	replaceFunc := func(s string) string {
		timestampStr := s[1 : len(s)-1] // Extract timestamp string
		timestamp, err := strconv.Atoi(timestampStr)
		if err != nil {
			return s // Return original string if conversion fails
		}
		t := time.Unix(int64(timestamp), 0)
		return fmt.Sprintf("%s", t.Format("15:04"))
	}
	forecast.Desc = timestampRe.ReplaceAllStringFunc(forecast.Desc, replaceFunc)
	return forecast, nil
}
