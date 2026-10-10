package cmd

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func resolveRequestLocation(r *http.Request) (Location, string, error) {
	q := r.URL.Query()
	name := strings.TrimSpace(q.Get("name"))
	latText, lonText := strings.TrimSpace(q.Get("lat")), strings.TrimSpace(q.Get("lon"))
	if latText == "" && lonText == "" {
		loc, err := ResolveLocationFor(0, 0, name)
		return loc, name, err
	}
	if latText == "" || lonText == "" {
		return Location{}, name, fmt.Errorf("provide both latitude and longitude")
	}
	lat, err := strconv.ParseFloat(latText, 64)
	if err != nil {
		return Location{}, name, fmt.Errorf("parse latitude: %w", err)
	}
	lon, err := strconv.ParseFloat(lonText, 64)
	if err != nil {
		return Location{}, name, fmt.Errorf("parse longitude: %w", err)
	}
	if err := validateCoordinates(lat, lon); err != nil {
		return Location{}, name, err
	}
	if label := strings.TrimSpace(q.Get("label")); label != "" {
		return Location{Latitude: lat, Longitude: lon, Description: label}, name, nil
	}
	loc, err := locationFromCoordinates(lat, lon)
	return loc, name, err
}

func locQuery(loc Location, name string) template.URL {
	q := url.Values{"lat": {coordinateText(loc.Latitude)}, "lon": {coordinateText(loc.Longitude)}, "label": {loc.Description}}
	if name != "" {
		q.Set("name", name)
	}
	return template.URL(q.Encode())
}
