package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// brightSkyRadarResponse is the part of https://api.brightsky.dev/radar this
// client reads: DWD's RV radar composite and nowcast for one 1 km cell.
type brightSkyRadarResponse struct {
	Radar []struct {
		Timestamp string `json:"timestamp"`
		// Precipitation in 0.01 mm per 5 minutes; one row of one cell with
		// distance=0.
		Precipitation [][]float64 `json:"precipitation_5"`
	} `json:"radar"`
}

// brightSkyToMmH converts 0.01 mm per 5 minutes to mm/h.
const brightSkyToMmH = 0.01 * 12

// GetDWDForecast returns DWD's 2-hour radar nowcast through Bright Sky,
// cached for two minutes. Bright Sky reports 0 outside DWD's radar coverage,
// which looks like dry weather, so only call it for points in Germany.
func GetDWDForecast(lat, lon float64) (*Forecast, error) {
	return memo(dwdCache, fmt.Sprintf("%.3f|%.3f", lat, lon), func() (*Forecast, error) {
		return getDWDForecastUncached(lat, lon, time.Now())
	})
}

func getDWDForecastUncached(lat, lon float64, now time.Time) (*Forecast, error) {
	// Without date, Bright Sky returns an hour either side of now; from now
	// it returns the latest run up to its 2-hour horizon.
	url := fmt.Sprintf("https://api.brightsky.dev/radar?lat=%.4f&lon=%.4f&distance=0&format=plain&date=%s",
		lat, lon, now.UTC().Truncate(5*time.Minute).Format(time.RFC3339))
	slog.Debug("dwd: requesting", "url", url)
	// The plain format must be compressed; Go's transport asks for gzip and
	// decompresses on its own.
	body, err := nowcastGet(url, "dwd", 10*time.Second)
	if err != nil {
		return nil, err
	}
	return parseBrightSkyRadar(body, now, nowcastZone(lat, lon))
}

func parseBrightSkyRadar(body []byte, now time.Time, zone *time.Location) (*Forecast, error) {
	var response brightSkyRadarResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode dwd radar: %w", err)
	}
	forecast := &Forecast{Desc: "DWD", Type: PrecipitationForecast, Source: "DWD"}
	for _, frame := range response.Radar {
		t, err := time.Parse(time.RFC3339, frame.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("dwd radar time %q: %w", frame.Timestamp, err)
		}
		if !t.After(now) || len(frame.Precipitation) == 0 || len(frame.Precipitation[0]) == 0 {
			continue
		}
		forecast.Data = append(forecast.Data, ForecastDataPoint{Time: t.In(zone), Value: frame.Precipitation[0][0] * brightSkyToMmH})
	}
	return forecast, nil
}

// nowcastGet fetches a nowcast provider's response body with the shared
// User-Agent, failing on any status but 200.
func nowcastGet(url, provider string, timeout time.Duration) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer closeBody(resp.Body, provider+" response body")
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: unexpected status code %d", provider, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// userAgent identifies this app to services that ask for it (Nominatim, MET
// Norway).
func userAgent() string {
	return "weather-cli/" + Version + " (+https://github.com/jsnjack/weather)"
}
