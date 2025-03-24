package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func GetDescriptionFromCoordinates(lat, lon float64) (string, error) {
	DebugLogger.Printf("Getting description from coordinates: lat %.2f, lon %.2f\n", lat, lon)
	urlTpl := "https://us1.api-bdc.net/data/reverse-geocode-client?latitude=%.2f&longitude=%.2f&localityLanguage=en"
	url := fmt.Sprintf(urlTpl, lat, lon)

	DebugLogger.Printf("Requesting %s\n", url)
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code %d", resp.StatusCode)
	}
	var response struct {
		Locality string `json:"locality"`
		Country  string `json:"countryCode"`
		City     string `json:"city"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}

	description := response.Locality
	return description, nil
}
