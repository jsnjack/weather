package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type OpenstreetmapResponse struct {
	PlaceID     int      `json:"place_id"`
	Licence     string   `json:"licence"`
	OsmType     string   `json:"osm_type"`
	OsmID       int      `json:"osm_id"`
	Lat         string   `json:"lat"`
	Lon         string   `json:"lon"`
	Class       string   `json:"class"`
	Type        string   `json:"type"`
	PlaceRank   int      `json:"place_rank"`
	Importance  float64  `json:"importance"`
	Addresstype string   `json:"addresstype"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	BoundingBox []string `json:"boundingbox"`
}

// GetLocationFromString resolves a place name and caches successful results.
func GetLocationFromString(str string) (Location, error) {
	str = strings.TrimSpace(str)
	if str == "" {
		return Location{}, fmt.Errorf("place name is empty")
	}
	return memo(locationNameCache, str, func() (Location, error) { return fetchLocationFromString(str) })
}

func fetchLocationFromString(str string) (Location, error) {
	slog.Debug("getting location from string", "name", str)
	location := Location{}
	client := &http.Client{
		Timeout: time.Second * 10,
	}

	reqURL := fmt.Sprintf("https://nominatim.openstreetmap.org/search?q=%s&format=json", url.QueryEscape(str))

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return location, fmt.Errorf("build nominatim request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	// Nominatim's usage policy requires a User-Agent identifying the app
	// and 403s anonymous Go-http-client requests on sight.
	req.Header.Set("User-Agent", "weather-cli/"+Version+" (+https://github.com/jsnjack/weather)")

	resp, err := client.Do(req)
	if err != nil {
		return location, fmt.Errorf("nominatim request: %w", err)
	}
	defer closeBody(resp.Body, "nominatim response body")

	if resp.StatusCode != http.StatusOK {
		return location, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	var osmResponses []OpenstreetmapResponse
	if err := json.NewDecoder(resp.Body).Decode(&osmResponses); err != nil {
		return location, err
	}

	if len(osmResponses) == 0 {
		return location, fmt.Errorf("no results found")
	}

	osmResponse := osmResponses[0]

	location.Description = osmResponse.DisplayName
	location.Longitude, err = strconv.ParseFloat(osmResponse.Lon, 64)
	if err != nil {
		return Location{}, fmt.Errorf("parse geocoded longitude: %w", err)
	}
	location.Latitude, err = strconv.ParseFloat(osmResponse.Lat, 64)
	if err != nil {
		return Location{}, fmt.Errorf("parse geocoded latitude: %w", err)
	}
	if err := validateCoordinates(location.Latitude, location.Longitude); err != nil {
		return Location{}, fmt.Errorf("geocoded location: %w", err)
	}

	return location, nil
}
