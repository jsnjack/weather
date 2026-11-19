package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"html/template"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var nowcastTestNow = time.Date(2026, 11, 19, 21, 7, 0, 0, time.UTC)

func TestParseBrightSkyRadar(t *testing.T) {
	body := []byte(`{"radar":[
		{"timestamp":"2026-11-19T21:05:00+00:00","precipitation_5":[[9]]},
		{"timestamp":"2026-11-19T21:10:00+00:00","precipitation_5":[[2]]},
		{"timestamp":"2026-11-19T21:15:00+00:00","precipitation_5":[]},
		{"timestamp":"2026-11-19T21:20:00+00:00","precipitation_5":[[0]]}]}`)
	forecast, err := parseBrightSkyRadar(body, nowcastTestNow, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	// The past frame and the empty grid are skipped; 2 × 0.01 mm per 5 min is 0.24 mm/h.
	if forecast.Source != sourceDWD || len(forecast.Data) != 2 || forecast.Data[0].Value != 0.24 || forecast.Data[1].Value != 0 {
		t.Fatalf("forecast = %+v", forecast)
	}
}

func TestParseGeoSphere(t *testing.T) {
	body := []byte(`{"reference_time":"2026-11-19T20:45+00:00",
		"timestamps":["2026-11-19T21:00+00:00","2026-11-19T21:15+00:00","2026-11-19T21:30+00:00","2026-11-19T21:45+00:00"],
		"features":[{"properties":{"parameters":{"rr":{"unit":"kg m-2","data":[0.5,0.25,null,0.0]}}}}]}`)
	zone := time.FixedZone("CET", 3600)
	forecast, err := parseGeoSphere(body, nowcastTestNow, zone)
	if err != nil {
		t.Fatal(err)
	}
	// 21:00 has passed and 21:30 is missing; mm per 15 minutes × 4 is mm/h.
	if forecast.Source != sourceGeoSphere || len(forecast.Data) != 2 || forecast.Data[0].Value != 1 || forecast.Data[0].Time.Format("15:04") != "22:15" {
		t.Fatalf("forecast = %+v", forecast)
	}
	if _, err := parseGeoSphere([]byte(`{"timestamps":[],"features":[]}`), nowcastTestNow, zone); err == nil {
		t.Fatal("a response without features should fail")
	}
}

func TestParseMetNoNowcast(t *testing.T) {
	response := func(coverage string) []byte {
		return []byte(`{"properties":{"meta":{"radar_coverage":"` + coverage + `"},"timeseries":[
			{"time":"2026-11-19T21:05:00Z","data":{"instant":{"details":{"precipitation_rate":3.0}}}},
			{"time":"2026-11-19T21:10:00Z","data":{"instant":{"details":{"precipitation_rate":0.9}}}},
			{"time":"2026-11-19T21:15:00Z","data":{"instant":{"details":{}}}}]}}`)
	}
	forecast, err := parseMetNoNowcast(response("ok"), nowcastTestNow, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if forecast.Source != sourceMetNo || len(forecast.Data) != 1 || forecast.Data[0].Value != 0.9 {
		t.Fatalf("forecast = %+v", forecast)
	}
	if _, err := parseMetNoNowcast(response("no coverage"), nowcastTestNow, time.UTC); err == nil {
		t.Fatal("a point without radar coverage should fail")
	}
}

func TestSecondNowcastSources(t *testing.T) {
	for _, tc := range []struct {
		name     string
		country  string
		lat, lon float64
		want     []string
	}{
		{"Amsterdam", "NL", 52.37, 4.90, []string{sourceBuienradar}},
		{"Brussels", "BE", 50.85, 4.35, []string{sourceBuienradar}},
		{"Berlin", "DE", 52.52, 13.40, []string{sourceDWD, sourceBuienradar}},
		{"Munich prefers DWD over GeoSphere", "DE", 48.14, 11.58, []string{sourceDWD, sourceBuienradar}},
		{"Innsbruck", "AT", 47.27, 11.39, []string{sourceGeoSphere}},
		{"Vienna", "AT", 48.21, 16.37, []string{sourceGeoSphere}},
		{"Oslo", "NO", 59.91, 10.75, []string{sourceMetNo}},
		{"Helsinki", "FI", 60.17, 24.94, []string{sourceMetNo}},
		{"Zurich, inside GeoSphere's grid", "CH", 47.37, 8.54, []string{sourceGeoSphere}},
		{"Geneva, outside it", "CH", 46.20, 6.14, nil},
		{"Minsk", "BY", 53.90, 27.56, nil},
		{"Warsaw", "PL", 52.23, 21.01, nil},
		{"unknown country in the Netherlands", "", 52.37, 4.90, []string{sourceBuienradar}},
		{"unknown country in Austria", "", 47.27, 11.39, []string{sourceBuienradar, sourceGeoSphere}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := secondNowcastSources(tc.country, tc.lat, tc.lon); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("sources = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGetSecondNowcast(t *testing.T) {
	previousFetchers, previousPlaces := secondNowcastFetchers, locationPlaceCache
	t.Cleanup(func() { secondNowcastFetchers, locationPlaceCache = previousFetchers, previousPlaces })
	future := []ForecastDataPoint{{Time: time.Now().Add(10 * time.Minute), Value: 0.5}}
	setCountry := func(lat, lon float64, country string) {
		locationPlaceCache = newTTLCache[reversePlace](time.Hour, 8)
		locationPlaceCache.put(coordinateText(lat)+"|"+coordinateText(lon), reversePlace{Description: "test", Country: country})
	}
	var calls []string
	secondNowcastFetchers = map[string]func(float64, float64) (*Forecast, error){
		sourceDWD: func(float64, float64) (*Forecast, error) {
			calls = append(calls, sourceDWD)
			return nil, errors.New("dwd down")
		},
		sourceBuienradar: func(float64, float64) (*Forecast, error) {
			calls = append(calls, sourceBuienradar)
			return &Forecast{Source: sourceBuienradar, Data: future}, nil
		},
	}
	t.Run("falls back to the next source", func(t *testing.T) {
		setCountry(51.0, 7.0, "DE")
		forecast, err := getSecondNowcast(51.0, 7.0)
		if err != nil || forecast.Source != sourceBuienradar || !reflect.DeepEqual(calls, []string{sourceDWD, sourceBuienradar}) {
			t.Fatalf("forecast %+v, err %v, calls %v", forecast, err, calls)
		}
	})
	t.Run("no source for the country", func(t *testing.T) {
		setCountry(53.9, 27.56, "BY")
		if _, err := getSecondNowcast(53.9, 27.56); !errors.Is(err, errNoSecondNowcast) {
			t.Fatalf("err = %v, want errNoSecondNowcast", err)
		}
	})
}

func TestFetchSecondNowcastWhenSlow(t *testing.T) {
	previousFetchers, previousWait, previousStaleWait, previousLast := secondNowcastFetchers, secondNowcastWait, secondNowcastStaleWait, lastSecondNowcast
	t.Cleanup(func() {
		secondNowcastFetchers, secondNowcastWait, secondNowcastStaleWait, lastSecondNowcast = previousFetchers, previousWait, previousStaleWait, previousLast
	})
	secondNowcastWait, secondNowcastStaleWait = 20*time.Millisecond, 10*time.Millisecond
	lastSecondNowcast = newTTLCache[*Forecast](time.Hour, 8)
	now := time.Now()
	release := make(chan struct{})
	var background sync.WaitGroup
	// Cleanups run last-registered first: let slow fetches finish before the
	// globals are restored.
	t.Cleanup(func() { close(release); background.Wait() })
	answer := &Forecast{Source: sourceGeoSphere, Data: []ForecastDataPoint{{Time: now.Add(-time.Minute), Value: 1}, {Time: now.Add(15 * time.Minute), Value: 2}}}
	secondNowcastFetchers = map[string]func(float64, float64) (*Forecast, error){
		sourceGeoSphere: func(float64, float64) (*Forecast, error) { return answer, nil },
	}
	if _, err := fetchSecondNowcast(sourceGeoSphere, 47.27, 11.39); err != nil {
		t.Fatal(err)
	}
	// Two slow fetches follow; count them before they start.
	background.Add(2)
	secondNowcastFetchers = map[string]func(float64, float64) (*Forecast, error){
		sourceGeoSphere: func(float64, float64) (*Forecast, error) {
			defer background.Done()
			<-release
			return answer, nil
		},
	}
	forecast, err := fetchSecondNowcast(sourceGeoSphere, 47.27, 11.39)
	if err != nil || len(forecast.Data) != 1 || forecast.Data[0].Value != 2 {
		t.Fatalf("a slow fetch should answer with the last good forecast's future points: %+v, %v", forecast, err)
	}
	if _, err := fetchSecondNowcast(sourceGeoSphere, 48.21, 16.37); err == nil {
		t.Fatal("a slow fetch with nothing stored should fail")
	}
}

func TestBuienalarmEstimate(t *testing.T) {
	var response BuienalarmResponse
	if err := json.Unmarshal([]byte(`{"data":[],"summary":{"source":"radar-world","available":true}}`), &response); err != nil {
		t.Fatal(err)
	}
	if response.Summary.Source != buienalarmWorldSource {
		t.Fatalf("source = %q", response.Summary.Source)
	}
	estimate := &Forecast{Source: "Buienalarm", Estimate: true}
	if got := estimate.labelOr("x"); got != "Buienalarm (estimate)" {
		t.Fatalf("label = %q", got)
	}
	var missing *Forecast
	if got := missing.labelOr("Buienradar"); got != "Buienradar" {
		t.Fatalf("nil label = %q", got)
	}
}

func TestIndexChartKeyNamesTheSources(t *testing.T) {
	data := indexData{ChartSVG: template.HTML("<svg></svg>"), ChartKey: []chartKeyItem{{Name: "Buienalarm", Color: buienalarmColor}, {Name: "GeoSphere", Color: buineradarColor}}}
	var body bytes.Buffer
	if err := indexBodyTmpl.Execute(&body, data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), ">GeoSphere</span>") || strings.Contains(body.String(), ">Buienradar</span>") {
		t.Fatal("the chart key should list the sources that drew a line")
	}
}
