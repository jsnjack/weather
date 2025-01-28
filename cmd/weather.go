package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Example: populat BuinealarmResponse struct from this data:
// {"success":true,"start":1737903300,"start_human":"15:55","temp":5,"delta":300,"precip":[0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0],"levels":{"light":0.25,"moderate":1,"heavy":2.5},"grid":{"x":348,"y":399},"source":"nl","bounds":{"N":55.973602,"E":10.856429,"S":48.895302,"W":0}}
type BuinealarmResponse struct {
	Success    bool      `json:"success"`
	Start      int64     `json:"start"`
	StartHuman string    `json:"start_human"`
	Temp       int       `json:"temp"`
	Delta      int       `json:"delta"`
	Precip     []float64 `json:"precip"`
	Levels     Levels    `json:"levels"`
	Source     string    `json:"source"`
}

type Levels struct {
	Light    float64 `json:"light"`
	Moderate float64 `json:"moderate"`
	Heavy    float64 `json:"heavy"`
}

type ForecasePoint struct {
	Time          time.Time
	Precipitation float64
}

type Forecast struct {
	Temperature int
	Data        []*ForecasePoint
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
	if maxPrecipitation > 0 {
		if maxPrecipitation <= 0.25 {
			rainInfo = "Light rain expected."
		} else if maxPrecipitation <= 1.0 {
			rainInfo = "Moderate rain expected."
		} else {
			rainInfo = "Heavy rain expected."
		}
		// When the next rain starts?
		for _, point := range f.Data {
			if point.Precipitation > 0 {
				rainInfo += fmt.Sprintf(" Next rain starts at %s.", point.Time.Format("15:04"))
				break
			}
		}
	}
	return rainInfo
}

func GetForecast(lat, long float64) (*Forecast, error) {
	DebugLogger.Printf("Getting forecast for lat %.4f, long %.4f\n", lat, long)
	url := fmt.Sprintf("https://cdn-secure.buienalarm.nl/api/3.4/forecast.php?lat=%f.4&lon=%f.4&region=nl&unit=mm/u", lat, long)

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

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

	if !buinealarmResponse.Success {
		return nil, fmt.Errorf("failed to get forecast")
	}

	forecast := &Forecast{
		Temperature: buinealarmResponse.Temp,
	}
	for i, precip := range buinealarmResponse.Precip {
		t := time.Unix(buinealarmResponse.Start+int64(i*300), 0)
		// Filter out data from the past
		if t.After(time.Now()) {
			forecast.Data = append(forecast.Data, &ForecasePoint{
				Time:          t,
				Precipitation: precip,
			})
		}
	}
	return forecast, nil
}
