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

	// Compose "Locality, City" (e.g. "Oost, Amsterdam") when both are
	// present and differ. The city is the wider context; the locality
	// is the named neighbourhood. When they're equal or one is missing,
	// fall back to whichever is set.
	parts := make([]string, 0, 3)
	if response.Locality != "" {
		parts = append(parts, response.Locality)
	}
	if response.City != "" && response.City != response.Locality {
		parts = append(parts, response.City)
	}

	// Country: strip parenthesised official-name qualifiers such as
	// "Netherlands (Kingdom of the)". They're correct but unhelpful in
	// a small UI.
	country := stripParenthesisedQualifier(response.CountryName)
	if country != "" {
		parts = append(parts, country)
	}

	description := ""
	for i, p := range parts {
		if i > 0 {
			description += ", "
		}
		description += p
	}
	return description, nil
}

// stripParenthesisedQualifier removes a trailing "( ... )" qualifier and any
// whitespace before it. Idempotent on strings that don't contain a paren.
func stripParenthesisedQualifier(s string) string {
	open := -1
	for i, r := range s {
		if r == '(' {
			open = i
			break
		}
	}
	if open < 0 {
		return s
	}
	// Trim trailing whitespace before the paren.
	trimmed := s[:open]
	for len(trimmed) > 0 && (trimmed[len(trimmed)-1] == ' ' || trimmed[len(trimmed)-1] == '\t') {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}
