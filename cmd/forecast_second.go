package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// The chart's second nowcast line comes from a national radar nowcast picked
// by country, so it works beyond Buienradar's Netherlands and Belgium.
// Countries without an open radar-based nowcast get no second line: Buienalarm
// (line 1) still answers there, labelled as an estimate.
const (
	sourceBuienradar = "Buienradar"
	sourceDWD        = "DWD"
	sourceGeoSphere  = "GeoSphere"
	sourceMetNo      = "MET Norway"
)

var errNoSecondNowcast = errors.New("no radar nowcast covers this location")

var (
	// secondNowcastWait is how long a page or widget refresh waits for one
	// source, or secondNowcastStaleWait when an earlier answer could stand in.
	// A slower fetch keeps running and fills the source's cache for the next
	// refresh; GeoSphere often needs 5–20 seconds.
	secondNowcastWait      = 6 * time.Second
	secondNowcastStaleWait = 1500 * time.Millisecond
	// lastSecondNowcast keeps each source's last good answer for when the
	// next fetch is slow.
	lastSecondNowcast = newTTLCache[*Forecast](30*time.Minute, 512)
)

var secondNowcastFetchers = map[string]func(lat, lon float64) (*Forecast, error){
	sourceBuienradar: GetBuineradarForecast,
	sourceDWD:        GetDWDForecast,
	sourceGeoSphere:  GetGeoSphereForecast,
	sourceMetNo:      GetMetNoForecast,
}

// secondNowcastSources lists the sources to try, in order, for a point in a
// country (ISO 3166-1 alpha-2; empty when reverse geocoding failed).
func secondNowcastSources(country string, lat, lon float64) []string {
	switch country {
	case "NL", "BE", "LU":
		return []string{sourceBuienradar}
	case "DE":
		// Buienradar also covers western Germany if DWD is down.
		return []string{sourceDWD, sourceBuienradar}
	case "AT":
		return []string{sourceGeoSphere}
	case "DK", "NO", "SE", "FI":
		return []string{sourceMetNo}
	case "":
		// Unknown country: Buienradar, as before, and GeoSphere's area.
		if inGeoSphereArea(lat, lon) {
			return []string{sourceBuienradar, sourceGeoSphere}
		}
		return []string{sourceBuienradar}
	}
	// GeoSphere's grid reaches into Switzerland, Liechtenstein, Italy,
	// Slovenia, Czechia, Slovakia and Hungary.
	if inGeoSphereArea(lat, lon) {
		return []string{sourceGeoSphere}
	}
	return nil
}

// getSecondNowcast returns the chart's second line from the first source
// that covers the point and answers with future data.
func getSecondNowcast(lat, lon float64) (*Forecast, error) {
	country, err := GetCountryFromCoordinates(lat, lon)
	if err != nil {
		slog.Debug("second nowcast: country unknown", "err", err)
		country = ""
	}
	var errs []error
	for _, source := range secondNowcastSources(country, lat, lon) {
		forecast, err := fetchSecondNowcast(source, lat, lon)
		if err == nil && forecast != nil && len(forecast.Data) > 0 {
			return forecast, nil
		}
		if err == nil {
			err = errors.New("no future data")
		}
		errs = append(errs, fmt.Errorf("%s: %w", source, err))
	}
	if len(errs) == 0 {
		return nil, errNoSecondNowcast
	}
	return nil, errors.Join(errs...)
}

// fetchSecondNowcast waits for a source up to secondNowcastWait, or
// secondNowcastStaleWait when it has a recent answer. When the source is
// slower, it returns that answer trimmed to the future and lets the fetch
// finish in the background.
func fetchSecondNowcast(source string, lat, lon float64) (*Forecast, error) {
	key := fmt.Sprintf("%s|%.2f|%.2f", source, lat, lon)
	fetch, last := secondNowcastFetchers[source], lastSecondNowcast
	var forecast *Forecast
	var err error
	done := make(chan struct{})
	go func() {
		defer close(done)
		forecast, err = fetch(lat, lon)
		if err == nil && forecast != nil && len(forecast.Data) > 0 {
			last.put(key, forecast)
		}
	}()
	wait := secondNowcastWait
	stale, hasStale := last.get(key)
	if hasStale {
		wait = secondNowcastStaleWait
	}
	select {
	case <-done:
		return forecast, err
	case <-time.After(wait):
		if hasStale {
			if future := futureForecast(stale, time.Now()); len(future.Data) > 0 {
				return future, nil
			}
		}
		return nil, fmt.Errorf("no answer within %s", wait)
	}
}

// futureForecast copies f without the points that have passed.
func futureForecast(f *Forecast, now time.Time) *Forecast {
	future := *f
	future.Data = nil
	for _, point := range f.Data {
		if point.Time.After(now) {
			future.Data = append(future.Data, point)
		}
	}
	return &future
}
