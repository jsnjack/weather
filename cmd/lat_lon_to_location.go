package cmd

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

func GetDescriptionFromCoordinates(lat, lon float64) (string, error) {
	slog.Debug("reverse-geocode: getting description", "lat", lat, "lon", lon)
	urlTpl := "https://us1.api-bdc.net/data/reverse-geocode-client?latitude=%.2f&longitude=%.2f&localityLanguage=en"
	url := fmt.Sprintf(urlTpl, lat, lon)

	slog.Debug("reverse-geocode: requesting", "url", url)
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer closeBody(resp.Body, "reverse-geocode response body")
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}
	var response struct {
		Locality    string `json:"locality"`
		Country     string `json:"countryCode"`
		CountryName string `json:"countryName"`
		City        string `json:"city"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}

	description := response.Locality
	if description == "" {
		description = response.City
	}
	if description != "" && response.CountryName != "" {
		description = description + ", " + response.CountryName
	}
	return description, nil
}
