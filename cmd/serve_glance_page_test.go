package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// glanceTestAlarm and glanceTestRadar are 5-minute nowcast values: rain from
// the fifth step, Buienalarm peaking at 1.4 mm/h and Buienradar at 1.1 mm/h.
var (
	glanceTestAlarm = []float64{0, 0, 0, 0, .1, .35, .7, 1.05, 1.3, 1.4, 1.25, .95, .7, .5, .35, .25, .15, .05, 0, 0, 0, 0, 0, 0, 0}
	glanceTestRadar = []float64{0, 0, 0, 0, 0, .05, .2, .45, .7, .9, 1.05, 1.1, 1, .8, .6, .45, .3, .2, .1, .05, 0, 0, 0, 0, 0}
)

func glanceTestNowcast(start time.Time, alarmValues, radarValues []float64) *glanceAPIResponse {
	alarm, radar := &Forecast{}, &Forecast{}
	for i, value := range alarmValues {
		alarm.Data = append(alarm.Data, ForecastDataPoint{Time: start.Add(time.Duration(i) * glanceNowcastStep), Value: value})
	}
	for i, value := range radarValues {
		radar.Data = append(radar.Data, ForecastDataPoint{Time: start.Add(time.Duration(i) * glanceNowcastStep), Value: value})
	}
	return &glanceAPIResponse{Buienalarm: alarm, Buineradar: radar, Condition: "overcast",
		Temperature: glancePair{Now: 12, End: 10}, FeelsLike: glancePair{Now: 9, End: 7},
		Wind: glanceWindPair{Now: glanceWind{SpeedKmh: 22, DirectionDeg: 265}, End: glanceWind{SpeedKmh: 31, DirectionDeg: 270}}}
}

// glanceTestSources is a complete set of sources for 4 November 2026 at
// 14:05 in Amsterdam. The current hour, inside the nowcast, carries a 73%
// rain chance that must never reach the 5-minute band.
func glanceTestSources(t *testing.T, todayRain bool) (glanceSources, time.Time) {
	t.Helper()
	zone, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	midnight := time.Date(2026, 11, 4, 0, 0, 0, 0, zone)
	now := midnight.Add(14*time.Hour + 5*time.Minute)
	data := &OpenMeteoData{}
	for i := range 7 * 24 {
		at := midnight.Add(time.Duration(i) * time.Hour)
		hour := HourlyForecast{Time: at, Temperature: 9, ApparentTemperature: 6, PrecipitationProbability: 12, WindSpeed: 18, WindGusts: 31, WindDirection: 270, WeatherCode: 3}
		if at.Day() == 4 {
			if at.Hour() == 14 {
				hour.PrecipitationProbability = 73
			}
			if todayRain {
				hour.Precipitation, hour.PrecipitationProbability = .9, 65
			}
		}
		data.Hourly = append(data.Hourly, hour)
	}
	var daily []DailyAggregate
	for i := range glanceForecastDays {
		day := midnight.AddDate(0, 0, i)
		sunrise, sunset := day.Add(7*time.Hour+37*time.Minute), day.Add(17*time.Hour+12*time.Minute)
		data.Daily = append(data.Daily, DailyForecast{Date: day, Sunrise: sunrise, Sunset: sunset})
		daily = append(daily, DailyAggregate{Date: day, Condition: "rain", TempMax: 10 + float64(i%4), TempMin: 4, PrecipSum: 0.4, PrecipProbMax: 30, WindMax: 24, GustMax: 41, Sunrise: sunrise, Sunset: sunset})
	}
	return glanceSources{glance: glanceTestNowcast(now, glanceTestAlarm, glanceTestRadar), hourly: data, daily: daily}, now
}

func renderGlancePage(t *testing.T, page glancePageData) string {
	t.Helper()
	var body bytes.Buffer
	if err := glanceHeadTmpl.Execute(&body, page); err != nil {
		t.Fatal(err)
	}
	if err := glanceBodyTmpl.Execute(&body, page); err != nil {
		t.Fatal(err)
	}
	return body.String()
}

func TestGlanceRainCell(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mm     float64
		chance int
		amount string
		wet    bool
	}{
		{"dry and unlikely", 0, 35, "dry", false},
		{"trace amount stays dry", 0.04, 85, "dry", true},
		{"wet from 40 percent", 0.4, 40, "0.4 mm", true},
		{"light rain below 40 percent", 0.6, 39, "0.6 mm", false},
		{"1 mm at a low chance", 1.0, 25, "1.0 mm", true},
		{"heavy rain at a low chance", 17, 29, "17 mm", true},
		{"whole millimetres from 10", 11.2, 90, "11 mm", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := makeGlanceRain(tc.mm, tc.chance)
			if got.Amount != tc.amount || got.Wet != tc.wet || got.Chance != tc.chance {
				t.Fatalf("rain cell = %+v", got)
			}
		})
	}
}

func TestGlanceNowcast(t *testing.T) {
	start := time.Date(2026, 11, 4, 13, 5, 0, 0, time.UTC)
	t.Run("rain events", func(t *testing.T) {
		band := buildGlanceNowcast(glanceTestNowcast(start, glanceTestAlarm, glanceTestRadar))
		var texts []string
		for _, event := range band.Events {
			texts = append(texts, event.Text)
		}
		want := []string{"Rain from 13:25", "Peak 1.4 mm/h", "Peak 1.1 mm/h", "Dry again ~14:45"}
		if strings.Join(texts, "|") != strings.Join(want, "|") {
			t.Fatalf("events = %q, want %q", texts, want)
		}
		if band.Dry || band.PlotSVG == "" || band.HeightPx != 24*glanceNowcastRowPx || band.End != "15:05" {
			t.Fatalf("band = dry %v, height %d, end %s", band.Dry, band.HeightPx, band.End)
		}
		for i := 1; i < len(band.Events); i++ {
			if gap := band.Events[i].Top + band.Events[i].Shift - band.Events[i-1].Top - band.Events[i-1].Shift; gap < glanceEventGapPx {
				t.Fatalf("labels %d and %d are %dpx apart", i-1, i, gap)
			}
		}
		if !strings.Contains(band.Summary, "Buienalarm peak 1.4 mm/h at 13:50") || !strings.Contains(band.Summary, "dry again around 14:45") {
			t.Fatalf("summary = %q", band.Summary)
		}
	})
	t.Run("dry window", func(t *testing.T) {
		band := buildGlanceNowcast(glanceTestNowcast(start, make([]float64, 25), make([]float64, 25)))
		if !band.Dry || band.PlotSVG != "" || len(band.Events) != 0 || band.Message != "No rain expected until 15:05" {
			t.Fatalf("band = %+v", band)
		}
	})
	t.Run("radar rain beyond the Buienalarm horizon", func(t *testing.T) {
		radar := append(make([]float64, 25), 3, 3)
		band := buildGlanceNowcast(glanceTestNowcast(start, make([]float64, 25), radar))
		if !band.Dry || band.End != "15:05" {
			t.Fatalf("band = dry %v until %s", band.Dry, band.End)
		}
	})
	t.Run("rain already falling", func(t *testing.T) {
		alarm := []float64{.5, .5, 0, 0}
		band := buildGlanceNowcast(glanceTestNowcast(start, alarm, nil))
		if len(band.Events) == 0 || band.Events[0].Text != "Rain now" || band.Events[0].Shift != glanceEventMinPx {
			t.Fatalf("events = %+v", band.Events)
		}
	})
	t.Run("no provider", func(t *testing.T) {
		band := buildGlanceNowcast(nil)
		if band.Note == "" || band.Message == "" || band.PlotSVG != "" {
			t.Fatalf("band = %+v", band)
		}
	})
}

func TestGlancePage(t *testing.T) {
	t.Run("hours continue after the nowcast", func(t *testing.T) {
		src, now := glanceTestSources(t, false)
		var page glancePageData
		populateGlancePage(&page, src, now)
		if len(page.Today.Rows) != 8 || page.Today.Rows[0].Time != "16:00" {
			t.Fatalf("hour rows = %d from %+v", len(page.Today.Rows), page.Today.Rows)
		}
		if len(page.Days.Rows) != glanceForecastDays-1 || page.Days.Rows[0].Label != "Thu 5" || page.Days.Rows[0].Week != 45 {
			t.Fatalf("day rows = %d, first %+v", len(page.Days.Rows), page.Days.Rows[0])
		}
		if len(page.Today.Sun) != 1 || page.Today.Sun[0].Time != "17:12" || len(page.Today.Night) != 1 {
			t.Fatalf("sun = %+v, night = %+v", page.Today.Sun, page.Today.Night)
		}
	})
	t.Run("no rain probability in the 2-hour band", func(t *testing.T) {
		src, now := glanceTestSources(t, false)
		var page glancePageData
		populateGlancePage(&page, src, now)
		body := renderGlancePage(t, page)
		start, end := strings.Index(body, `id="g-nowcast"`), strings.Index(body, `id="g-today"`)
		if start < 0 || end < start {
			t.Fatal("2-hour band missing")
		}
		if band := body[start:end]; strings.Contains(band, "g-dp") || strings.Contains(band, "73%") {
			t.Fatal("2-hour band shows Open-Meteo rain chance")
		}
		if strings.Contains(body, "73%") {
			t.Fatal("rain chance for hours inside the nowcast is shown")
		}
	})
	t.Run("today's ride windows stay with today", func(t *testing.T) {
		src, now := glanceTestSources(t, false)
		var page glancePageData
		populateGlancePage(&page, src, now)
		if len(page.Today.Rides) != 1 || page.Today.Rides[0].Hours != "15–17" || page.Today.RideNote != "" || page.Days.Rows[0].Rides != nil {
			t.Fatalf("today rides %+v, note %q, tomorrow %+v", page.Today.Rides, page.Today.RideNote, page.Days.Rows[0].Rides)
		}
		if body := renderGlancePage(t, page); !strings.Contains(body, "Ride windows today") || !strings.Contains(body, "<b>15–17</b>") {
			t.Fatal("today's ride windows not rendered")
		}
	})
	t.Run("ride windows fall back to tomorrow", func(t *testing.T) {
		src, now := glanceTestSources(t, true)
		var page glancePageData
		populateGlancePage(&page, src, now)
		if len(page.Today.Rides) != 0 || page.Today.RideNote != "No dry 2-hour ride window left in daylight" || len(page.Days.Rows[0].Rides) != rideWindowLimit {
			t.Fatalf("today rides %+v, note %q, tomorrow %+v", page.Today.Rides, page.Today.RideNote, page.Days.Rows[0].Rides)
		}
		body := renderGlancePage(t, page)
		tomorrow, chips := strings.Index(body, ">Thu 5<"), strings.Index(body, `class="g-rides"`)
		if tomorrow < 0 || chips < tomorrow || strings.Index(body, ">Fri 6<") < chips {
			t.Fatal("tomorrow's ride windows are not under tomorrow's row")
		}
	})
	t.Run("tomorrow's windows stay in the hourly band without daily data", func(t *testing.T) {
		src, now := glanceTestSources(t, true)
		src.daily, src.dailyErr = nil, errors.New("daily down")
		var page glancePageData
		populateGlancePage(&page, src, now)
		if len(page.Today.TomorrowRides) != rideWindowLimit || page.Days.Note == "" {
			t.Fatalf("tomorrow rides %+v, days note %q", page.Today.TomorrowRides, page.Days.Note)
		}
	})
	t.Run("shared temperature scale", func(t *testing.T) {
		src, now := glanceTestSources(t, false)
		var page glancePageData
		populateGlancePage(&page, src, now)
		hour, day := page.Today.Rows[0].Temp, page.Days.Rows[0].Temp
		// Both bands place 9 °C at the same position.
		if hour.DotStyle != glanceCSS("left:%.2f%%", page.Scale.pos(9)) || !strings.Contains(string(day.Labels[0].Style), fmt.Sprintf("%.2f", 100-page.Scale.pos(4))) {
			t.Fatalf("hour %+v, day %+v", hour, day)
		}
	})
}

// glanceTransport answers the nowcast providers and Open-Meteo relative to the
// real clock, because the providers drop points that are not in the future.
func glanceTransport(t *testing.T, hourlyCalls *atomic.Int32, dailyStatus int) http.RoundTripper {
	zone, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(zone)
	first := now.Truncate(glanceNowcastStep).Add(glanceNowcastStep)
	return locationTransportFunc(func(r *http.Request) (*http.Response, error) {
		var body any
		status := http.StatusOK
		switch {
		case r.URL.Host == "imn-rust-lb.infoplaza.io":
			alarm := BuienalarmResponse{NowcastMessage: NowcastMessage{En: fmt.Sprintf("Rain expected from {%d}", first.Add(4*glanceNowcastStep).Unix())}}
			for i, value := range glanceTestAlarm {
				alarm.Data = append(alarm.Data, PrecipitationData{PrecipitationRate: value, Timestamp: first.Add(time.Duration(i) * glanceNowcastStep).Unix()})
			}
			body = alarm
		case r.URL.Host == "graphdata.buienradar.nl":
			var radar BuineradarResponse
			for i, value := range glanceTestRadar {
				radar.Forecasts = append(radar.Forecasts, RadarForecast{UtcDateTime: first.Add(time.Duration(i) * glanceNowcastStep).UTC().Format("2006-01-02T15:04:05"), DataValue: value})
			}
			body = radar
		case r.URL.Host == "api.open-meteo.com" && r.URL.Query().Get("hourly") != "":
			hourlyCalls.Add(1)
			response := openMeteoRangeResponse{Timezone: zone.String()}
			midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
			for at := midnight; at.Before(midnight.AddDate(0, 0, 7)); at = at.Add(time.Hour) {
				chance := 20
				h := &response.Hourly
				h.Time = append(h.Time, at.Format("2006-01-02T15:04"))
				h.Temperature2m = append(h.Temperature2m, 9)
				h.ApparentTemp = append(h.ApparentTemp, 6)
				h.Precipitation = append(h.Precipitation, 0)
				h.PrecipProb = append(h.PrecipProb, &chance)
				h.WindSpeed10m = append(h.WindSpeed10m, 18)
				h.WindDirection10m = append(h.WindDirection10m, 270)
				h.WindGusts10m = append(h.WindGusts10m, 31)
				h.UvIndex = append(h.UvIndex, 1)
				h.WeatherCode = append(h.WeatherCode, 3)
			}
			for day := midnight; day.Before(midnight.AddDate(0, 0, 7)); day = day.AddDate(0, 0, 1) {
				response.Daily.Time = append(response.Daily.Time, day.Format("2006-01-02"))
				response.Daily.Sunrise = append(response.Daily.Sunrise, day.Format("2006-01-02")+"T07:40")
				response.Daily.Sunset = append(response.Daily.Sunset, day.Format("2006-01-02")+"T17:10")
			}
			body = response
		case r.URL.Host == "api.open-meteo.com":
			status = dailyStatus
			response := openMeteoDailyResponse{Timezone: zone.String()}
			d := &response.Daily
			for i := range glanceForecastDays {
				date := now.AddDate(0, 0, i).Format("2006-01-02")
				chance := 30
				d.Time = append(d.Time, date)
				d.WeatherCode = append(d.WeatherCode, 61)
				d.TempMax = append(d.TempMax, 11)
				d.TempMin = append(d.TempMin, 5)
				d.FeelsMax = append(d.FeelsMax, 9)
				d.FeelsMin = append(d.FeelsMin, 2)
				d.PrecipSum = append(d.PrecipSum, 1.2)
				d.PrecipProbMax = append(d.PrecipProbMax, &chance)
				d.WindMax = append(d.WindMax, 24)
				d.GustMax = append(d.GustMax, 41)
				d.WindDirDominant = append(d.WindDirDominant, 250)
				d.UVMax = append(d.UVMax, 1)
				d.Sunrise = append(d.Sunrise, date+"T07:40")
				d.Sunset = append(d.Sunset, date+"T17:10")
			}
			body = response
		default:
			return nil, fmt.Errorf("unexpected upstream: %s", r.URL.Host)
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			encoded = []byte(`{"error":true,"reason":"test failure"}`)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(encoded))}, nil
	})
}

func TestGlanceHandler(t *testing.T) {
	const query = "/glance?lat=52.37&lon=4.9&label=Amsterdam"
	for _, tc := range []struct {
		name        string
		dailyStatus int
		want        []string
		dayRows     int
	}{
		{"renders every band", http.StatusOK, []string{"Next 2 hours", "Peak 1.4 mm/h", "Rest of today", "Next 13 days", "How to read it", `id="radar-player"`, `data-outline="30"`, `class="g-read"`}, glanceForecastDays - 1},
		{"daily failure keeps the other bands", http.StatusBadRequest, []string{"Next 2 hours", "Peak 1.4 mm/h", "Rest of today", "Unable to fetch the daily forecast right now.", "How to read it", `class="g-read"`}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetOpenMeteo(t)
			previousAlarm, previousRadar := buienalarmCache, buineradarCache
			buienalarmCache, buineradarCache = newTTLCache[*Forecast](time.Minute, 8), newTTLCache[*Forecast](time.Minute, 8)
			t.Cleanup(func() { buienalarmCache, buineradarCache = previousAlarm, previousRadar })
			var hourlyCalls atomic.Int32
			http.DefaultTransport = glanceTransport(t, &hourlyCalls, tc.dailyStatus)

			w := httptest.NewRecorder()
			handleGlance(w, httptest.NewRequest("GET", query, nil))
			body := w.Body.String()
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d", w.Code)
			}
			for _, want := range tc.want {
				if !strings.Contains(body, want) {
					t.Errorf("missing %q", want)
				}
			}
			if got := strings.Count(body, `class="g-row g-day`); got != tc.dayRows {
				t.Errorf("day rows = %d, want %d", got, tc.dayRows)
			}
			if hourlyCalls.Load() != 1 {
				t.Errorf("hourly Open-Meteo requests = %d, want 1 shared request", hourlyCalls.Load())
			}
		})
	}
}

func TestGlanceNightIcons(t *testing.T) {
	zone := time.FixedZone("CET", 3600)
	days := []DailyForecast{{
		Date:    time.Date(2026, 11, 5, 0, 0, 0, 0, zone),
		Sunrise: time.Date(2026, 11, 5, 7, 41, 0, 0, zone),
		Sunset:  time.Date(2026, 11, 5, 17, 3, 0, 0, zone),
	}}
	for _, tc := range []struct {
		hour      int
		condition string
		want      string
	}{
		{6, "clear", "night"},
		{7, "clear", "night"}, // sunrise 07:41: most of the hour is still dark
		{8, "clear", "clear"},
		{16, "clear", "clear"},
		{17, "clear", "night"},
		{21, "partly_cloudy", "overcast"},
		{21, "rain", "rain"},
	} {
		at := time.Date(2026, 11, 5, tc.hour, 0, 0, 0, zone)
		if got := conditionIconKey(tc.condition, glanceNight(days, at)); got != tc.want {
			t.Errorf("%02d:00 %s = %s, want %s", tc.hour, tc.condition, got, tc.want)
		}
	}
	if glanceNight(nil, time.Date(2026, 11, 5, 23, 0, 0, 0, zone)) {
		t.Error("unknown sun times should count as day")
	}
}
