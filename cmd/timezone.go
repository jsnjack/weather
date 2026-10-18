package cmd

import (
	"fmt"
	"time"
)

// Relative forecasts let Open-Meteo choose today's date in the location's zone.
func getLocationForecast(lat, lon float64) (*OpenMeteoData, error) {
	requestURL := fmt.Sprintf(openMeteoHourlyURL+"&forecast_days=7", lat, lon)
	body, err := openMeteoGetBody(requestURL)
	if err != nil {
		return nil, err
	}
	return parseOpenMeteoRange(body, lat, lon)
}

func resolveForecastZone(lat, lon float64) (*time.Location, error) {
	if zone, ok := locationZones.Load(zoneKey(lat, lon)); ok {
		return zone.(*time.Location), nil
	}
	data, err := getLocationForecast(lat, lon)
	if err != nil {
		return nil, fmt.Errorf("resolve forecast timezone: %w", err)
	}
	if len(data.Hourly) == 0 {
		return nil, fmt.Errorf("resolve forecast timezone: no hourly data")
	}
	return data.Hourly[0].Time.Location(), nil
}

func forecastInZone(f *Forecast, zone *time.Location) *Forecast {
	if f == nil {
		return nil
	}
	copy := *f
	copy.Data = make([]ForecastDataPoint, len(f.Data))
	for i, point := range f.Data {
		copy.Data[i] = ForecastDataPoint{Time: point.Time.In(zone), Value: point.Value}
	}
	return &copy
}

// termplt formats Unix timestamps in time.Local. Only CLI commands call this;
// HTTP handlers keep each timestamp's own zone and never change process state.
func cliTimezone(zone *time.Location) func() {
	previous := time.Local
	time.Local = zone
	return func() { time.Local = previous }
}

// These nowcast providers cover the Netherlands and neighbouring countries.
// Their regional clock remains usable when the global forecast is unavailable.
func nowcastZone(lat, lon float64) *time.Location {
	if zone, ok := locationZones.Load(zoneKey(lat, lon)); ok {
		return zone.(*time.Location)
	}
	zone, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		return time.UTC
	}
	return zone
}

func localHour(at time.Time) time.Time {
	return at.Add(-time.Duration(at.Minute())*time.Minute - time.Duration(at.Second())*time.Second - time.Duration(at.Nanosecond()))
}
