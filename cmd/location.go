package cmd

import "fmt"

// Location represents a location to show the weather for
type Location struct {
	Description string  `json:"description"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

// ResolveLocationFor picks a location using (in priority order) explicit
// lat/lon, a place name, or IP-based geolocation as fallback. Same precedence
// as ResolveLocation, but parameterised so it is safe to call concurrently
// (e.g. from HTTP handlers) without depending on package-global flags.
func ResolveLocationFor(lat, lon float64, name string) (Location, error) {
	if lat != 0 || lon != 0 {
		desc, err := GetDescriptionFromCoordinates(lat, lon)
		if err != nil {
			DebugLogger.Printf("Error getting description from coordinates: %s\n", err)
			desc = fmt.Sprintf("Lat %.2f, Lon %.2f", lat, lon)
		}
		return Location{
			Latitude:    lat,
			Longitude:   lon,
			Description: desc,
		}, nil
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
