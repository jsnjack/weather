package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const MAXMIND_URL = "https://geoip.maxmind.com/geoip/v2.1/city/me"

// Location represents a location to show the weather for
type Location struct {
	Description string
	Latitude    float64
	Longitude   float64
}

type MaxMindResponse struct {
	City struct {
		GeonameID int `json:"geoname_id"`
		Names     struct {
			En string `json:"en"`
		} `json:"names"`
	} `json:"city"`
	Continent struct {
		Code      string `json:"code"`
		GeonameID int    `json:"geoname_id"`
		Names     struct {
			En string `json:"en"`
		} `json:"names"`
	} `json:"continent"`
	Country struct {
		IsInEuropeanUnion bool   `json:"is_in_european_union"`
		IsoCode           string `json:"iso_code"`
		GeonameID         int    `json:"geoname_id"`
		Names             struct {
			En string `json:"en"`
		} `json:"names"`
	} `json:"country"`
	Location struct {
		AccuracyRadius int     `json:"accuracy_radius"`
		Latitude       float64 `json:"latitude"`
		Longitude      float64 `json:"longitude"`
		TimeZone       string  `json:"time_zone"`
	} `json:"location"`
	Postal struct {
		Code string `json:"code"`
	} `json:"postal"`
	RegisteredCountry struct {
		IsInEuropeanUnion bool   `json:"is_in_european_union"`
		IsoCode           string `json:"iso_code"`
		GeonameID         int    `json:"geoname_id"`
		Names             struct {
			En string `json:"en"`
		} `json:"names"`
	} `json:"registered_country"`
	Subdivisions []struct {
		IsoCode   string `json:"iso_code"`
		GeonameID int    `json:"geoname_id"`
		Names     struct {
			En string `json:"en"`
		} `json:"names"`
	} `json:"subdivisions"`
	Traits struct {
		AutonomousSystemNumber       int    `json:"autonomous_system_number"`
		AutonomousSystemOrganization string `json:"autonomous_system_organization"`
		ConnectionType               string `json:"connection_type"`
		Isp                          string `json:"isp"`
		Organization                 string `json:"organization"`
		IpAddress                    string `json:"ip_address"`
		Network                      string `json:"network"`
	} `json:"traits"`
}

func GetLocationFromIP() (*Location, error) {
	client := &http.Client{
		Timeout: time.Second * 10,
	}

	req, err := http.NewRequest("GET", MAXMIND_URL, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", "https://www.maxmind.com/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}

	var maxMindResponse MaxMindResponse
	if err := json.NewDecoder(resp.Body).Decode(&maxMindResponse); err != nil {
		return nil, err
	}
	desc := fmt.Sprintf("%s, %s (±%dm)", maxMindResponse.City.Names.En, maxMindResponse.Country.Names.En, maxMindResponse.Location.AccuracyRadius)
	location := &Location{
		Description: desc,
		Longitude:   maxMindResponse.Location.Longitude,
		Latitude:    maxMindResponse.Location.Latitude,
	}

	return location, nil
}
