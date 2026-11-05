package cmd

import (
	"testing"
	"time"
)

func TestNextGlanceHoursAndRain(t *testing.T) {
	zone := time.FixedZone("CET", 3600)
	at := func(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, zone) }
	var hourly []HourlyForecast
	for h := 12; h < 24; h++ {
		hourly = append(hourly, HourlyForecast{Time: at(5, h), Temperature: float64(24 - h), WeatherCode: 0})
	}
	hourly = append(hourly, HourlyForecast{Time: at(6, 0), Temperature: 4, WeatherCode: 61, Precipitation: 0.6})
	daily := []DailyForecast{{Date: at(5, 0), Sunrise: at(5, 7).Add(41 * time.Minute), Sunset: at(5, 17).Add(3 * time.Minute)}}
	now := at(5, 14).Add(5 * time.Minute)

	hours := nextGlanceHours(hourly, daily, now)
	if len(hours) != glanceHourCount {
		t.Fatalf("got %d hours, want %d", len(hours), glanceHourCount)
	}
	if hours[0].Time != at(5, 15).Format(time.RFC3339) || hours[0].Temperature != 9 || hours[0].Condition != "clear" {
		t.Fatalf("first hour = %+v, want 15:00, 9°, clear", hours[0])
	}
	if hours[1].Night || !hours[2].Night {
		t.Fatalf("night flags = %v, %v; want 16:00 day and 17:00 night (sunset 17:03)", hours[1].Night, hours[2].Night)
	}

	if nightAt(daily, now) || !nightAt(daily, at(5, 17).Add(10*time.Minute)) || !nightAt(daily, at(5, 7)) {
		t.Fatal("nightAt: 14:05 is day, 17:10 and 07:00 are night")
	}

	rain := nextGlanceRain(hourly, now.Add(2*time.Hour))
	if rain == nil || rain.Time != at(6, 0).Format(time.RFC3339) || rain.MM != 0.6 {
		t.Fatalf("next rain = %+v, want 00:00 tomorrow with 0.6 mm", rain)
	}
	hourly[5].Precipitation = 0.05 // 17:00: a trace, below the threshold
	hourly[2].Precipitation = 2    // 14:00: inside the nowcast window, ignored
	if got := nextGlanceRain(hourly, now.Add(2*time.Hour)); got == nil || got.Time != at(6, 0).Format(time.RFC3339) {
		t.Fatalf("next rain = %+v; traces and nowcast-window rain must not count", got)
	}
	if got := nextGlanceRain(hourly[:12], now.Add(2*time.Hour)); got != nil {
		t.Fatalf("dry forecast gave next rain %+v", got)
	}
}
