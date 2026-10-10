package cmd

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"
)

// Location represents a location to show the weather for
type Location struct {
	Description string  `json:"description"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

const geocodingTTL = 24 * time.Hour

var (
	locationNameCache        = newTTLCache[Location](geocodingTTL, 1024)
	locationDescriptionCache = newTTLCache[string](geocodingTTL, 1024)
)

func coordinateText(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func validateCoordinates(lat, lon float64) error {
	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
		return fmt.Errorf("latitude must be a finite number between -90 and 90")
	}
	if math.IsNaN(lon) || math.IsInf(lon, 0) || lon < -180 || lon > 180 {
		return fmt.Errorf("longitude must be a finite number between -180 and 180")
	}
	return nil
}

func locationFromCoordinates(lat, lon float64) (Location, error) {
	if err := validateCoordinates(lat, lon); err != nil {
		return Location{}, err
	}
	desc, err := GetDescriptionFromCoordinates(lat, lon)
	if err != nil {
		slog.Debug("reverse-geocode failed; using fallback description", "err", err)
		desc = fmt.Sprintf("Lat %s, Lon %s", coordinateText(lat), coordinateText(lon))
	}
	return Location{Latitude: lat, Longitude: lon, Description: desc}, nil
}

// ResolveLocationFor picks a location using (in priority order) explicit
// lat/lon, a place name, or IP-based geolocation as fallback. Same precedence
// as ResolveLocation, but parameterised so it is safe to call concurrently
// (e.g. from HTTP handlers) without depending on package-global flags.
func ResolveLocationFor(lat, lon float64, name string) (Location, error) {
	if lat != 0 || lon != 0 {
		return locationFromCoordinates(lat, lon)
	}
	if name != "" {
		return GetLocationFromString(name)
	}
	return GetLocationFromIP()
}

// ResolveLocation reads the CLI flag globals and delegates to
// ResolveLocationFor. Kept as a thin wrapper so existing CLI commands work
// unchanged.
func ResolveLocation() (Location, error) {
	return ResolveLocationFor(FlagLat, FlagLon, FlagStrLocation)
}
