package cmd

import "fmt"

// Location represents a location to show the weather for
type Location struct {
	Description string
	Latitude    float64
	Longitude   float64
}

// ResolveLocation picks the start location using (in priority order) explicit
// lat/lon flags, a place name, or IP-based geolocation as fallback.
func ResolveLocation() (Location, error) {
	if FlagLat != 0 || FlagLon != 0 {
		desc, err := GetDescriptionFromCoordinates(FlagLat, FlagLon)
		if err != nil {
			DebugLogger.Printf("Error getting description from coordinates: %s\n", err)
			desc = fmt.Sprintf("Lat %.2f, Lon %.2f", FlagLat, FlagLon)
		}
		return Location{
			Latitude:    FlagLat,
			Longitude:   FlagLon,
			Description: desc,
		}, nil
	}
	if FlagStrLocation != "" {
		return GetLocationFromString(FlagStrLocation)
	}
	return GetLocationFromIP()
}
