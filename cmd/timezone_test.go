package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestForecastTimezone(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, instant, clock string }{
		{"summer", "2026-07-01T10:00:00Z", "12:00"},
		{"winter", "2026-12-01T10:00:00Z", "11:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, tc.instant)
			if err != nil {
				t.Fatal(err)
			}
			original := &Forecast{Data: []ForecastDataPoint{{Time: at, Value: 1}}}
			local := forecastInZone(original, zone)
			if !local.Data[0].Time.Equal(at) || local.Data[0].Time.Format("15:04") != tc.clock {
				t.Fatalf("local forecast = %v", local.Data)
			}
			if original.Data[0].Time.Location() != time.UTC {
				t.Fatal("mutated shared forecast")
			}
			svg := string(RenderLineChartSVG([]SVGSeries{{Name: "rain", Data: append(local.Data, ForecastDataPoint{Time: at.Add(time.Hour).In(zone), Value: 1})}}, SVGOpts{}))
			if !strings.Contains(svg, ">"+tc.clock+"</text>") {
				t.Fatalf("chart has no local time %s", tc.clock)
			}
		})
	}
}

func TestNowcastMessageTimezone(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	// 2026-07-01 10:00 UTC is noon in Amsterdam.
	at := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	message := formatNowcastMessage("Rain at {1782900000}", zone)
	if at.Unix() != 1782900000 || message != "Rain at 12:00" {
		t.Fatalf("message=%q timestamp=%d", message, at.Unix())
	}
}

func TestRideScoringUsesInstants(t *testing.T) {
	zone, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		start time.Time
	}{
		{"different timezone", time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)},
		{"repeated autumn hour", time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hours := []HourlyForecast{
				{Time: tc.start.In(zone), WindSpeed: 10},
				{Time: tc.start.Add(time.Hour).In(zone), Precipitation: 2, WindSpeed: 30},
			}
			cell := scoreRideCell(hours, tc.start, 2)
			if cell.NoData || cell.DryHours != 1 || cell.MaxPrecip != 2 || cell.WindSpeed != 30 {
				t.Fatalf("score=%+v", cell)
			}
			wind := extractHourlyWind(hours, tc.start, 2)
			if len(wind) != 2 || wind[0].Speed != 10 || wind[1].Speed != 30 {
				t.Fatalf("wind=%+v", wind)
			}
		})
	}
}

func TestLocalHour(t *testing.T) {
	for _, value := range []string{"2026-07-01T12:42:13+05:45", "2026-10-25T02:42:13+02:00", "2026-10-25T02:42:13+01:00"} {
		t.Run(value, func(t *testing.T) {
			at, err := time.Parse(time.RFC3339, value)
			if err != nil {
				t.Fatal(err)
			}
			got := localHour(at)
			if got.Hour() != at.Hour() || got.Minute() != 0 || at.Sub(got) != 42*time.Minute+13*time.Second {
				t.Fatalf("hour=%v", got)
			}
		})
	}
}
