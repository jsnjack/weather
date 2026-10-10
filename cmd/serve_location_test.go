package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocationNavigation(t *testing.T) {
	for _, tc := range []struct {
		name, place string
		loc         Location
	}{
		{"named place", "Amsterdam", Location{Latitude: 52.3730796, Longitude: 4.8924534, Description: "Amsterdam, Noord-Holland, Nederland"}},
		{"coordinates", "", Location{Latitude: -33.123456789, Longitude: 18.987654321, Description: "Cape Town & surroundings"}},
		{"zero coordinates", "", Location{Latitude: 0, Longitude: 0, Description: "Equator"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := string(locQuery(tc.loc, tc.place))
			for _, route := range []string{"/", "/day", "/tomorrow", "/weekend", "/hourly", "/forecast", "/today", "/multiday"} {
				r := httptest.NewRequest("GET", route+"?"+query, nil)
				loc, name, err := resolveRequestLocation(r)
				if err != nil || loc != tc.loc || name != tc.place {
					t.Fatalf("%s: location=%+v name=%q error=%v", route, loc, name, err)
				}
				query = string(locQuery(loc, name))
			}
		})
	}
}

func TestInvalidLocationRequests(t *testing.T) {
	for _, query := range []string{"lat=abc&lon=4", "lat=52", "lon=4", "lat=NaN&lon=4", "lat=52&lon=Inf", "lat=91&lon=4", "lat=52&lon=-181", "lat=abc&lon=4&name=Amsterdam"} {
		t.Run(query, func(t *testing.T) {
			for _, handler := range []http.HandlerFunc{handleIndex, handleHourly, handleOutlook, handleForecast, handleToday, handleMultiday, handleRainJSON, handleGlanceJSON, handleOutlookJSON, handleTodayJSON, handleMultidayJSON} {
				w := httptest.NewRecorder()
				handler(w, httptest.NewRequest("GET", "/?"+query, nil))
				if w.Code != http.StatusBadRequest {
					t.Fatalf("status=%d, body=%s", w.Code, w.Body.String())
				}
			}
		})
	}
}

type locationTransportFunc func(*http.Request) (*http.Response, error)

func (fn locationTransportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

func TestGeocodingCache(t *testing.T) {
	oldTransport, oldNames, oldDescriptions := http.DefaultTransport, locationNameCache, locationDescriptionCache
	t.Cleanup(func() {
		http.DefaultTransport, locationNameCache, locationDescriptionCache = oldTransport, oldNames, oldDescriptions
	})
	locationNameCache = newTTLCache[Location](geocodingTTL, 1024)
	locationDescriptionCache = newTTLCache[string](geocodingTTL, 1024)
	forward, reverse := 0, 0
	http.DefaultTransport = locationTransportFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Host {
		case "nominatim.openstreetmap.org":
			forward++
			body = `[{"lat":"52.3730796","lon":"4.8924534","display_name":"Amsterdam, Nederland"}]`
		case "us1.api-bdc.net":
			reverse++
			if r.URL.Query().Get("latitude") != "52.3730796" || r.URL.Query().Get("longitude") != "4.8924534" {
				t.Errorf("rounded reverse-geocoding coordinates: %s", r.URL.RawQuery)
			}
			body = `{"locality":"Centrum","city":"Amsterdam","countryName":"Netherlands"}`
		default:
			return nil, fmt.Errorf("unexpected upstream: %s", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	t.Run("name cached across views", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			loc, name, err := resolveRequestLocation(httptest.NewRequest("GET", "/day?name=Amsterdam", nil))
			if err != nil || loc.Description != "Amsterdam, Nederland" || name != "Amsterdam" {
				t.Fatalf("location=%+v name=%q error=%v", loc, name, err)
			}
		}
		if forward != 1 {
			t.Fatalf("name lookups=%d, want 1", forward)
		}
	})
	t.Run("coordinates cached without rounding", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			loc, _, err := resolveRequestLocation(httptest.NewRequest("GET", "/day?lat=52.3730796&lon=4.8924534", nil))
			if err != nil || loc.Description != "Centrum, Amsterdam, Netherlands" {
				t.Fatalf("location=%+v error=%v", loc, err)
			}
		}
		if reverse != 1 {
			t.Fatalf("reverse lookups=%d, want 1", reverse)
		}
	})
}
