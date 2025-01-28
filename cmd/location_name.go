package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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

func GetLocationFromString(str string) (Location, error) {
	DebugLogger.Printf("Getting location from string: %s\n", str)
	location := Location{}
	client := &http.Client{
		Timeout: time.Second * 10,
	}

	reqURL := fmt.Sprintf("https://nominatim.openstreetmap.org/search?q=%s&format=json", url.QueryEscape(str))

	req, err := http.NewRequest("GET", reqURL, nil)
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return location, err
	}
	defer resp.Body.Close()

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
	location.Longitude = parseFloat(osmResponse.Lon)
	location.Latitude = parseFloat(osmResponse.Lat)

	return location, nil
}

func parseFloat(str string) float64 {
	value, _ := strconv.ParseFloat(str, 64)
	return value
}
