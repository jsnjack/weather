package cmd

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// Chart colours for the forecast pages. Warm orange for temperature / daily
// highs, cool blue for feels-like / daily lows. Rain reuses the Buienalarm
// cyan from serve.go so precipitation reads consistently across pages.
const (
	tempColor  = "#f97316"
	feelsColor = "#60a5fa"
)

// ---------- shared Open-Meteo daily fetch ----------

// DailyAggregate is one calendar day of summary weather used by the 14-day
// page. All times are in the grid cell's local zone (timezone=auto).
type DailyAggregate struct {
	Date            time.Time
	WeatherCode     int
	Condition       string // wmoCondition token
	TempMax         float64
	TempMin         float64
	FeelsMax        float64
	FeelsMin        float64
	PrecipSum       float64 // mm over the day
	PrecipProbMax   int     // 0-100
	WindMax         float64 // km/h sustained
	GustMax         float64 // km/h
	WindDirDominant float64 // degrees the wind comes FROM
	UVMax           float64
	Sunrise         time.Time
	Sunset          time.Time
}

type openMeteoDailyResponse struct {
	Timezone  string `json:"timezone"` // IANA zone of the wall-clock strings (timezone=auto)
	UTCOffset int    `json:"utc_offset_seconds"`
	Daily     struct {
		Time            []string  `json:"time"`
		WeatherCode     []int     `json:"weather_code"`
		TempMax         []float64 `json:"temperature_2m_max"`
		TempMin         []float64 `json:"temperature_2m_min"`
		FeelsMax        []float64 `json:"apparent_temperature_max"`
		FeelsMin        []float64 `json:"apparent_temperature_min"`
		PrecipSum       []float64 `json:"precipitation_sum"`
		PrecipProbMax   []*int    `json:"precipitation_probability_max"`
		WindMax         []float64 `json:"wind_speed_10m_max"`
		GustMax         []float64 `json:"wind_gusts_10m_max"`
		WindDirDominant []float64 `json:"wind_direction_10m_dominant"`
		UVMax           []float64 `json:"uv_index_max"`
		Sunrise         []string  `json:"sunrise"`
		Sunset          []string  `json:"sunset"`
	} `json:"daily"`
}

// openMeteoGetBody runs the same retry/back-off policy as GetOpenMeteoRange and
// returns the raw response body. Factored out so the daily endpoint shares the
// transient-failure handling without duplicating the loop.
func openMeteoGetBody(url string) ([]byte, error) {
	slog.Debug("open-meteo: requesting", "url", url)
	client := &http.Client{Timeout: 15 * time.Second}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		r, err := client.Get(url)
		switch {
		case err != nil:
			lastErr = err
			slog.Debug("open-meteo net error", "attempt", attempt+1, "err", err)
		case r.StatusCode == http.StatusTooManyRequests || r.StatusCode >= 500:
			lastErr = fmt.Errorf("open-meteo transient status %d", r.StatusCode)
			closeBody(r.Body, "open-meteo transient response")
			slog.Debug("open-meteo transient status", "status", r.StatusCode, "attempt", attempt+1)
		default:
			defer closeBody(r.Body, "open-meteo response body")
			if r.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(r.Body)
				return nil, fmt.Errorf("open-meteo status %d: %s", r.StatusCode, string(body))
			}
			return io.ReadAll(r.Body)
		}
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}
	return nil, fmt.Errorf("open-meteo retries exhausted: %w", lastErr)
}

// GetOpenMeteoDailyRange fetches per-day summary weather for `days` days
// starting today. Like GetOpenMeteoRange it requests timezone=auto so day
// boundaries and sun times are local. Cached process-wide (openMeteoDailyCache);
// the returned slice MUST be treated as read-only.
func GetOpenMeteoDailyRange(lat, lon float64, days int) ([]DailyAggregate, error) {
	if days < 1 {
		days = 1
	}
	if days > 16 { // Open-Meteo caps the free daily horizon at 16 days
		days = 16
	}
	key := fmt.Sprintf("%.3f|%.3f|%d", lat, lon, days)
	return memo(openMeteoDailyCache, key, func() ([]DailyAggregate, error) {
		return getOpenMeteoDailyRangeUncached(lat, lon, days)
	})
}

func getOpenMeteoDailyRangeUncached(lat, lon float64, days int) ([]DailyAggregate, error) {
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f"+
			"&daily=weather_code,temperature_2m_max,temperature_2m_min,apparent_temperature_max,apparent_temperature_min,"+
			"precipitation_sum,precipitation_probability_max,wind_speed_10m_max,wind_gusts_10m_max,wind_direction_10m_dominant,"+
			"uv_index_max,sunrise,sunset&timezone=auto&forecast_days=%d",
		lat, lon, days,
	)
	body, err := openMeteoGetBody(url)
	if err != nil {
		return nil, err
	}
	var parsed openMeteoDailyResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}

	d := parsed.Daily
	n := len(d.Time)
	// Reject ragged responses rather than zero-filling — mirrors the hourly
	// path in GetOpenMeteoRange.
	if len(d.WeatherCode) != n || len(d.TempMax) != n || len(d.TempMin) != n ||
		len(d.FeelsMax) != n || len(d.FeelsMin) != n || len(d.PrecipSum) != n ||
		len(d.PrecipProbMax) != n || len(d.WindMax) != n || len(d.GustMax) != n ||
		len(d.WindDirDominant) != n || len(d.UVMax) != n ||
		len(d.Sunrise) != n || len(d.Sunset) != n {
		return nil, fmt.Errorf("open-meteo returned inconsistent daily array lengths")
	}

	// Parse wall-clock strings in the zone Open-Meteo reports for the
	// location, so the times are correct instants on any server (see
	// openMeteoZone in scout_fetch.go).
	zone := openMeteoZone(parsed.Timezone, parsed.UTCOffset)
	rememberZone(lat, lon, zone)

	out := make([]DailyAggregate, 0, n)
	for i, ds := range d.Time {
		day, err := time.ParseInLocation("2006-01-02", ds, zone)
		if err != nil {
			slog.Debug("open-meteo daily: skipping unparseable date", "date", ds, "err", err)
			continue
		}
		prob := 0
		if d.PrecipProbMax[i] != nil {
			prob = *d.PrecipProbMax[i]
		}
		// Sun times are optional — parse best-effort.
		sr, _ := time.ParseInLocation("2006-01-02T15:04", d.Sunrise[i], zone)
		ss, _ := time.ParseInLocation("2006-01-02T15:04", d.Sunset[i], zone)
		out = append(out, DailyAggregate{
			Date:            day,
			WeatherCode:     d.WeatherCode[i],
			Condition:       wmoCondition(d.WeatherCode[i]),
			TempMax:         d.TempMax[i],
			TempMin:         d.TempMin[i],
			FeelsMax:        d.FeelsMax[i],
			FeelsMin:        d.FeelsMin[i],
			PrecipSum:       d.PrecipSum[i],
			PrecipProbMax:   prob,
			WindMax:         d.WindMax[i],
			GustMax:         d.GustMax[i],
			WindDirDominant: d.WindDirDominant[i],
			UVMax:           d.UVMax[i],
			Sunrise:         sr,
			Sunset:          ss,
		})
	}
	return out, nil
}

// windClassFor / uvClassFor mirror makeWindView / makeUVView so the new
// forecast pages colour wind and UV with the same caution thresholds as the
// rain page, the glance API, and the Android widget.
func windClassFor(kmh int) string {
	switch {
	case kmh >= WindCriticalKmh:
		return "critical"
	case kmh >= WindCautionKmh:
		return "caution"
	default:
		return "muted"
	}
}

func uvClassFor(uv int) string {
	switch {
	case uv >= UVCritical:
		return "critical"
	case uv >= UVCaution:
		return "caution"
	default:
		return "muted"
	}
}

// ---------- /hourly (by-hour day forecast) ----------

type hourlyRow struct {
	Time      string // "15:00", prefixed with weekday on the first hour of a new day
	NewDay    bool   // marks the first row of a calendar day for a subtle rule
	Temp      int
	Feels     int
	Precip    string // formatted mm (blank when ~0)
	PrecipPct int
	WindArrow string
	WindKmh   int
	WindClass string
	UV        int
	UVClass   string
	Condition string // human label
}

type hourlyPageData struct {
	Location       Location
	Q              template.URL
	NameInput      string
	Hours          int
	StartLabel     string
	EndLabel       string
	TempChartSVG   template.HTML
	PrecipChartSVG template.HTML
	Rows           []hourlyRow
	Now            string
	Note           string // populated when the upstream fetch failed entirely
}

func parseHoursParam(r *http.Request) int {
	hours := 24
	if v := r.URL.Query().Get("hours"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 6 && n <= 48 {
			hours = n
		}
	}
	return hours
}

func handleHourly(w http.ResponseWriter, r *http.Request) {
	lat, lon, name := locationQuery(r)
	loc, err := ResolveLocationFor(lat, lon, name)
	if err != nil {
		http.Error(w, "could not resolve location: "+err.Error(), http.StatusBadRequest)
		return
	}
	hours := parseHoursParam(r)
	// Label the window in the location's wall clock (best-known zone; the
	// head streams before any fetch, so a first visit falls back to the
	// server zone).
	now := time.Now().In(locationZone(loc.Latitude, loc.Longitude))
	start := now.Truncate(time.Hour)
	end := start.Add(time.Duration(hours) * time.Hour)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)

	page := hourlyPageData{
		Location:   loc,
		Q:          locQuery(loc),
		NameInput:  name,
		Hours:      hours,
		StartLabel: start.Format("Mon 15:04"),
		EndLabel:   end.Format("Mon 15:04"),
	}
	if err := hourlyHeadTmpl.Execute(w, page); err != nil {
		slog.Debug("template execute", "tmpl", "hourlyHead", "err", err)
		return
	}
	if flusher != nil {
		flusher.Flush()
	}

	prog := NoProgress
	if flusher != nil {
		prog = NewHTTPProgress(w, flusher)
	}
	prog.AddTotal(1)
	// Fetch a little past the window so the last requested hour is covered even
	// across the local-midnight day boundary. GetOpenMeteoRange is date-granular
	// so it returns whole local days spanning [now, end].
	data, fetchErr := GetOpenMeteoRange(loc.Latitude, loc.Longitude, now, end.Add(2*time.Hour))
	prog.Inc(1)
	prog.Finish()

	page.Now = time.Now().Format("15:04:05")
	if fetchErr != nil || data == nil || len(data.Hourly) == 0 {
		page.Note = "Unable to fetch the hourly forecast right now."
		if err := hourlyBodyTmpl.Execute(w, page); err != nil {
			slog.Debug("template execute", "tmpl", "hourlyBody", "err", err)
		}
		return
	}

	var tempPts, feelsPts, precipPts []ForecastDataPoint
	lastDay := -1
	for _, h := range data.Hourly {
		if h.Time.Before(start) || h.Time.After(end) {
			continue
		}
		windKmh := int(round(h.WindSpeed))
		uv := int(round(h.UVIndex))
		newDay := h.Time.YearDay() != lastDay
		lastDay = h.Time.YearDay()
		label := h.Time.Format("15:04")
		if newDay {
			label = h.Time.Format("Mon 15:04")
		}
		page.Rows = append(page.Rows, hourlyRow{
			Time:      label,
			NewDay:    newDay && len(page.Rows) > 0,
			Temp:      int(round(h.Temperature)),
			Feels:     int(round(h.ApparentTemperature)),
			Precip:    formatPrecip(h.Precipitation),
			PrecipPct: h.PrecipitationProbability,
			WindArrow: windArrowFor(int(round(h.WindDirection))),
			WindKmh:   windKmh,
			WindClass: windClassFor(windKmh),
			UV:        uv,
			UVClass:   uvClassFor(uv),
			Condition: conditionHumanLabel(wmoCondition(h.WeatherCode)),
		})
		tempPts = append(tempPts, ForecastDataPoint{Time: h.Time, Value: h.Temperature})
		feelsPts = append(feelsPts, ForecastDataPoint{Time: h.Time, Value: h.ApparentTemperature})
		precipPts = append(precipPts, ForecastDataPoint{Time: h.Time, Value: h.Precipitation})
	}

	if len(tempPts) >= 2 {
		page.TempChartSVG = RenderLineChartSVG([]SVGSeries{
			{Name: "Temp", Color: tempColor, Data: tempPts},
			{Name: "Feels", Color: feelsColor, Data: feelsPts},
		}, SVGOpts{YUnit: "°C", XTimeFormat: "Mon 15h"})
		page.PrecipChartSVG = RenderLineChartSVG([]SVGSeries{
			{Name: "Precip", Color: buienalarmColor, Data: precipPts},
		}, SVGOpts{YUnit: "mm", XTimeFormat: "Mon 15h", MinYHi: 1, FillArea: true})
	}

	if err := hourlyBodyTmpl.Execute(w, page); err != nil {
		slog.Debug("template execute", "tmpl", "hourlyBody", "err", err)
	}
}

// ---------- /forecast (14-day) ----------

type dailyRow struct {
	Date      string // "Mon 27 May"
	Condition string
	TempMax   int
	TempMin   int
	FeelsMax  int
	FeelsMin  int
	Precip    string // mm sum, blank when ~0
	PrecipPct int
	WindArrow string
	WindKmh   int
	WindClass string
	GustKmh   int
	UV        int
	UVClass   string
	// Temperature range bar, positioned within the global min..max span so the
	// column reads as a sparkline of warming/cooling across the fortnight.
	BarLeftPct  float64
	BarWidthPct float64
}

type forecastPageData struct {
	Location     Location
	Q            template.URL
	NameInput    string
	Days         int
	StartLabel   string
	EndLabel     string
	TempChartSVG template.HTML
	Rows         []dailyRow
	Now          string
	Note         string
}

func parseDaysParam(r *http.Request) int {
	days := 14
	if v := r.URL.Query().Get("days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 3 && n <= 16 {
			days = n
		}
	}
	return days
}

func handleForecast(w http.ResponseWriter, r *http.Request) {
	lat, lon, name := locationQuery(r)
	loc, err := ResolveLocationFor(lat, lon, name)
	if err != nil {
		http.Error(w, "could not resolve location: "+err.Error(), http.StatusBadRequest)
		return
	}
	days := parseDaysParam(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)

	page := forecastPageData{
		Location:  loc,
		Q:         locQuery(loc),
		NameInput: name,
		Days:      days,
	}
	if err := forecastHeadTmpl.Execute(w, page); err != nil {
		slog.Debug("template execute", "tmpl", "forecastHead", "err", err)
		return
	}
	if flusher != nil {
		flusher.Flush()
	}

	prog := NoProgress
	if flusher != nil {
		prog = NewHTTPProgress(w, flusher)
	}
	prog.AddTotal(1)
	daily, fetchErr := GetOpenMeteoDailyRange(loc.Latitude, loc.Longitude, days)
	prog.Inc(1)
	prog.Finish()

	page.Now = time.Now().Format("15:04:05")
	if fetchErr != nil || len(daily) == 0 {
		page.Note = "Unable to fetch the 14-day forecast right now."
		if err := forecastBodyTmpl.Execute(w, page); err != nil {
			slog.Debug("template execute", "tmpl", "forecastBody", "err", err)
		}
		return
	}

	page.StartLabel = daily[0].Date.Format("Mon 2 Jan")
	page.EndLabel = daily[len(daily)-1].Date.Format("Mon 2 Jan")

	// Global temperature span drives the per-row range bars.
	gMin, gMax := daily[0].TempMin, daily[0].TempMax
	for _, d := range daily {
		if d.TempMin < gMin {
			gMin = d.TempMin
		}
		if d.TempMax > gMax {
			gMax = d.TempMax
		}
	}
	span := gMax - gMin
	if span < 1 {
		span = 1
	}

	var maxPts, minPts []ForecastDataPoint
	for _, d := range daily {
		windKmh := int(round(d.WindMax))
		uv := int(round(d.UVMax))
		page.Rows = append(page.Rows, dailyRow{
			Date:        d.Date.Format("Mon 2 Jan"),
			Condition:   conditionHumanLabel(d.Condition),
			TempMax:     int(round(d.TempMax)),
			TempMin:     int(round(d.TempMin)),
			FeelsMax:    int(round(d.FeelsMax)),
			FeelsMin:    int(round(d.FeelsMin)),
			Precip:      formatPrecip(d.PrecipSum),
			PrecipPct:   d.PrecipProbMax,
			WindArrow:   windArrowFor(int(round(d.WindDirDominant))),
			WindKmh:     windKmh,
			WindClass:   windClassFor(windKmh),
			GustKmh:     int(round(d.GustMax)),
			UV:          uv,
			UVClass:     uvClassFor(uv),
			BarLeftPct:  (d.TempMin - gMin) / span * 100,
			BarWidthPct: (d.TempMax - d.TempMin) / span * 100,
		})
		// Anchor each day's point at local noon so the line reads as one
		// sample per day rather than at midnight edges.
		noon := time.Date(d.Date.Year(), d.Date.Month(), d.Date.Day(), 12, 0, 0, 0, time.Local)
		maxPts = append(maxPts, ForecastDataPoint{Time: noon, Value: d.TempMax})
		minPts = append(minPts, ForecastDataPoint{Time: noon, Value: d.TempMin})
	}

	if len(maxPts) >= 2 {
		page.TempChartSVG = RenderLineChartSVG([]SVGSeries{
			{Name: "High", Color: tempColor, Data: maxPts},
			{Name: "Low", Color: feelsColor, Data: minPts},
		}, SVGOpts{YUnit: "°C", XTimeFormat: "Mon 2"})
	}

	if err := forecastBodyTmpl.Execute(w, page); err != nil {
		slog.Debug("template execute", "tmpl", "forecastBody", "err", err)
	}
}

// formatPrecip renders a precipitation amount for a table cell: blank below a
// hair (so dry rows stay quiet), one decimal under 10 mm, whole numbers above.
func formatPrecip(mm float64) string {
	switch {
	case mm < 0.05:
		return ""
	case mm < 10:
		return fmt.Sprintf("%.1f", mm)
	default:
		return fmt.Sprintf("%.0f", mm)
	}
}
