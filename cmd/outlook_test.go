package cmd

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOutlookBounds(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, period, date, start, end string
		hours                          int
	}{
		{"today", outlookToday, "2026-10-10", "2026-10-10", "2026-10-11", 24},
		{"tomorrow", outlookTomorrow, "2026-10-10", "2026-10-11", "2026-10-12", 24},
		{"weekday weekend", outlookWeekend, "2026-10-05", "2026-10-10", "2026-10-12", 48},
		{"Saturday remaining", outlookWeekend, "2026-10-10", "2026-10-10", "2026-10-12", 48},
		{"Sunday remaining", outlookWeekend, "2026-10-11", "2026-10-11", "2026-10-12", 24},
		{"spring DST", outlookToday, "2026-03-29", "2026-03-29", "2026-03-30", 23},
		{"autumn DST", outlookToday, "2026-10-25", "2026-10-25", "2026-10-26", 25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now, err := time.ParseInLocation("2006-01-02", tc.date, zone)
			if err != nil {
				t.Fatal(err)
			}
			start, end, err := outlookBounds(tc.period, now)
			if err != nil {
				t.Fatal(err)
			}
			if start.Format("2006-01-02") != tc.start || end.Format("2006-01-02") != tc.end || int(end.Sub(start).Hours()) != tc.hours {
				t.Fatalf("bounds = %v to %v", start, end)
			}
		})
	}
}

func TestRideWindows(t *testing.T) {
	start := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		modify func([]HourlyForecast)
		want   int
	}{
		{"dry daylight", func(h []HourlyForecast) {}, 3},
		{"rain chance at limit", func(h []HourlyForecast) {
			for i := range h {
				h[i].PrecipitationProbability = 30
			}
		}, 3},
		{"rain chance above limit", func(h []HourlyForecast) {
			for i := range h {
				h[i].PrecipitationProbability = 31
			}
		}, 0},
		{"certain rain with trace precipitation", func(h []HourlyForecast) {
			for i := range h {
				h[i].PrecipitationProbability = 100
				h[i].Precipitation = 0.01
			}
		}, 0},
		{"rain", func(h []HourlyForecast) {
			for i := range h {
				h[i].Precipitation = 1
			}
		}, 0},
		{"dangerous gusts", func(h []HourlyForecast) {
			for i := range h {
				h[i].WindGusts = 55
			}
		}, 0},
		{"missing hours", func(h []HourlyForecast) {
			for i := 1; i < len(h); i++ {
				h[i].Time = h[i].Time.Add(time.Duration(i) * time.Hour)
			}
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hours := make([]HourlyForecast, 6)
			for i := range hours {
				hours[i] = HourlyForecast{Time: start.Add(time.Duration(i) * time.Hour), ApparentTemperature: 18, WindSpeed: 10, WindGusts: 15}
			}
			tc.modify(hours)
			days := []DailyForecast{{Sunrise: start, Sunset: start.Add(12 * time.Hour)}}
			got := rankRideWindows(hours, days, start)
			if len(got) != tc.want {
				t.Fatalf("windows = %d, want %d", len(got), tc.want)
			}
			for i, ride := range got {
				if ride.Start.Before(start) || ride.End.After(days[0].Sunset) {
					t.Fatal("outside daylight")
				}
				for _, other := range got[i+1:] {
					if ride.Start.Before(other.End) && ride.End.After(other.Start) {
						t.Fatal("overlapping windows")
					}
				}
			}
		})
	}
	t.Run("past rides and night excluded", func(t *testing.T) {
		hours := []HourlyForecast{{Time: start}, {Time: start.Add(time.Hour)}}
		if got := rankRideWindows(hours, []DailyForecast{{Sunrise: start.Add(3 * time.Hour), Sunset: start.Add(10 * time.Hour)}}, start); len(got) != 0 {
			t.Fatal(got)
		}
		if got := rankRideWindows(hours, []DailyForecast{{Sunrise: start, Sunset: start.Add(10 * time.Hour)}}, start.Add(time.Hour)); len(got) != 0 {
			t.Fatal(got)
		}
	})
}

func TestBuildDetailedOutlook(t *testing.T) {
	t.Run("location zone, exclusive midnight and hidden past hours", func(t *testing.T) {
		zone, err := time.LoadLocation("Europe/Amsterdam")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 10, 9, 23, 30, 0, 0, time.UTC)
		start := time.Date(2026, 10, 10, 0, 0, 0, 0, zone)
		data := &OpenMeteoData{}
		for i := range 49 {
			data.Hourly = append(data.Hourly, HourlyForecast{Time: start.Add(time.Duration(i) * time.Hour)})
		}
		result, err := buildDetailedOutlook(data, outlookToday, now)
		if err != nil {
			t.Fatal(err)
		}
		// 01:30 local: midnight has ended, the current 01:00 hour stays.
		if len(result.Hours) != 23 || !result.Start.Equal(start) || !result.Hours[0].Time.Equal(start.Add(time.Hour)) {
			t.Fatalf("outlook = %v, %d hours from %v", result.Start, len(result.Hours), result.Hours[0].Time)
		}
		tomorrow, err := buildDetailedOutlook(data, outlookTomorrow, now)
		if err != nil {
			t.Fatal(err)
		}
		if len(tomorrow.Hours) != 24 || !tomorrow.Hours[0].Time.Equal(start.AddDate(0, 0, 1)) {
			t.Fatalf("tomorrow lost hours: %d from %v", len(tomorrow.Hours), tomorrow.Hours[0].Time)
		}
	})
	t.Run("invalid period", func(t *testing.T) {
		if _, _, err := outlookBounds("bad", time.Now()); err == nil {
			t.Fatal("accepted bad period")
		}
	})
}

func TestOutlookTemplates(t *testing.T) {
	for _, period := range []string{outlookToday, outlookTomorrow, outlookWeekend} {
		t.Run(period, func(t *testing.T) {
			start := time.Date(2026, 10, 10, 8, 0, 0, 0, time.UTC)
			result := detailedOutlook{Period: period, Start: start, End: start.AddDate(0, 0, 1), Hours: []HourlyForecast{{Time: start, WindGusts: 35}, {Time: start.Add(time.Hour)}}, Rides: []rideWindow{{Start: start, End: start.Add(2 * time.Hour)}}}
			page := hourlyPageData{Title: period, Period: period}
			populateOutlookPage(&page, result)
			var body bytes.Buffer
			if err := hourlyHeadTmpl.Execute(&body, page); err != nil {
				t.Fatal(err)
			}
			if err := hourlyBodyTmpl.Execute(&body, page); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"Best dry 2-hour ride windows", "Gusts", "/day", "/tomorrow", "/weekend", "35"} {
				if !strings.Contains(body.String(), want) {
					t.Errorf("missing %q", want)
				}
			}
			if _, err := json.Marshal(result); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("API rejects invalid period before fetching", func(t *testing.T) {
		response := httptest.NewRecorder()
		handleOutlookJSON(response, httptest.NewRequest("GET", "/api/v1/outlook?period=bad", nil))
		if response.Code != 400 {
			t.Fatalf("status = %d", response.Code)
		}
	})
}

func TestOpenMeteoRepeatedDSTHour(t *testing.T) {
	response := openMeteoRangeResponse{Timezone: "Europe/Amsterdam"}
	response.Hourly.Time = []string{"2026-10-25T00:00", "2026-10-25T01:00", "2026-10-25T02:00", "2026-10-25T02:00", "2026-10-25T03:00"}
	n := len(response.Hourly.Time)
	response.Hourly.Temperature2m = make([]float64, n)
	response.Hourly.ApparentTemp = make([]float64, n)
	response.Hourly.Precipitation = make([]float64, n)
	response.Hourly.PrecipProb = make([]*int, n)
	response.Hourly.WindSpeed10m = make([]float64, n)
	response.Hourly.WindDirection10m = make([]float64, n)
	response.Hourly.WindGusts10m = make([]float64, n)
	response.Hourly.UvIndex = make([]float64, n)
	response.Hourly.WeatherCode = make([]int, n)
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	data, err := parseOpenMeteoRange(body, 52.37, 4.90)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(data.Hourly); i++ {
		if gap := data.Hourly[i].Time.Sub(data.Hourly[i-1].Time); gap != time.Hour {
			t.Fatalf("hour %d gap = %s", i, gap)
		}
	}
}
