package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"
)

// GeoSphere Austria's INCA nowcast grid: 1 km cells, 15-minute steps, about
// 2.5 hours ahead, a new run every 15 minutes that arrives 20–25 minutes
// late. It blends radar, stations, a model and terrain, and covers Austria
// plus border strips of its neighbours. CC BY 4.0, credit GeoSphere Austria.
const (
	geosphereSouth, geosphereNorth = 45.50, 49.48
	geosphereWest, geosphereEast   = 8.10, 17.74
)

type geosphereResponse struct {
	Timestamps []string `json:"timestamps"`
	Features   []struct {
		Properties struct {
			Parameters struct {
				// Precipitation in kg m⁻², i.e. mm per 15 minutes.
				RR struct {
					Data []*float64 `json:"data"`
				} `json:"rr"`
			} `json:"parameters"`
		} `json:"properties"`
	} `json:"features"`
}

func inGeoSphereArea(lat, lon float64) bool {
	return lat >= geosphereSouth && lat <= geosphereNorth && lon >= geosphereWest && lon <= geosphereEast
}

// GetGeoSphereForecast returns GeoSphere Austria's nowcast for a point,
// cached for five minutes: runs are 15 minutes apart and the API allows 240
// requests an hour.
func GetGeoSphereForecast(lat, lon float64) (*Forecast, error) {
	if !inGeoSphereArea(lat, lon) {
		return nil, fmt.Errorf("geosphere: outside the nowcast area")
	}
	return memo(geosphereCache, fmt.Sprintf("%.2f|%.2f", lat, lon), func() (*Forecast, error) {
		// Rounded to the 1 km grid, so nearby points share GeoSphere's own
		// cache: an uncached request takes 5–20 seconds, a cached one about 1.
		url := fmt.Sprintf("https://dataset.api.hub.geosphere.at/v1/timeseries/forecast/nowcast-v1-15min-1km?lat_lon=%.2f,%.2f&parameters=rr&output_format=geojson", lat, lon)
		slog.Debug("geosphere: requesting", "url", url)
		body, err := nowcastGet(url, "geosphere", 30*time.Second)
		if err != nil {
			return nil, err
		}
		return parseGeoSphere(body, time.Now(), nowcastZone(lat, lon))
	})
}

func parseGeoSphere(body []byte, now time.Time, zone *time.Location) (*Forecast, error) {
	var response geosphereResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode geosphere nowcast: %w", err)
	}
	if len(response.Features) == 0 {
		return nil, fmt.Errorf("geosphere: no data for this point")
	}
	values := response.Features[0].Properties.Parameters.RR.Data
	forecast := &Forecast{Desc: "GeoSphere", Type: PrecipitationForecast, Source: "GeoSphere"}
	for i, stamp := range response.Timestamps {
		t, err := time.Parse("2006-01-02T15:04Z07:00", stamp)
		if err != nil {
			return nil, fmt.Errorf("geosphere time %q: %w", stamp, err)
		}
		if i >= len(values) || values[i] == nil || !t.After(now) {
			continue
		}
		forecast.Data = append(forecast.Data, ForecastDataPoint{Time: t.In(zone), Value: *values[i] * 4})
	}
	return forecast, nil
}
