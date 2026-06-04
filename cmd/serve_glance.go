package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Caution thresholds — match the Android widget's ChartRenderer so the web
// page, the home-screen widget, and the CLI all agree on when a metric is
// worth highlighting. Beaufort 5+ for wind, sunscreen-recommended for UV.
const (
	WindCautionKmh  = 28
	WindCriticalKmh = 50
	UVCaution       = 3
	UVCritical      = 8
	// DryThresholdMmH: when both providers stay below this across the
	// whole window we render the dry hero ("23° → 21° · clear") instead
	// of an empty chart. Matches the Android widget.
	DryThresholdMmH = 0.05
)

// glanceAPIResponse is the single-fetch payload consumed by the Android
// widget. Combines the precipitation nowcast (from Buienalarm/Buienradar)
// with a snapshot of temperature + weather condition from Open-Meteo so
// the widget only needs one HTTP round-trip per refresh.
//
// Precipitation probability is intentionally not exposed here: over the
// buinealarm 2h window it contradicts the precise rain line and adds noise.
type glanceAPIResponse struct {
	Location    Location       `json:"location"`
	Buienalarm  *Forecast      `json:"buienalarm"`
	Buineradar  *Forecast      `json:"buineradar"`
	Temperature glancePair     `json:"temperature"` // °C
	FeelsLike   glancePair     `json:"feels_like"`  // °C (apparent_temperature)
	Wind        glanceWindPair `json:"wind"`        // km/h + degrees-from-N
	UVIndex     glancePair     `json:"uv_index"`    // integer 0..11+
	Condition   string         `json:"condition"`
	Sun         []sunEvent     `json:"sun"`    // events within [now, now+2h], empty if none
	Sunset      string         `json:"sunset"` // next sunset, RFC3339 local; "" if unknown
}

type sunEvent struct {
	Kind string `json:"kind"` // "sunrise" | "sunset"
	Time string `json:"time"` // RFC3339 with offset, local zone
}

// glancePair carries a scalar at "now" and at "now + 2h", as integers so
// the widget can render them without further formatting.
type glancePair struct {
	Now int `json:"now"`
	End int `json:"end"`
}

type glanceWind struct {
	SpeedKmh     int `json:"speed_kmh"`
	DirectionDeg int `json:"direction_deg"` // meteorological — degrees the wind is coming FROM
}

type glanceWindPair struct {
	Now glanceWind `json:"now"`
	End glanceWind `json:"end"`
}

func handleGlanceJSON(w http.ResponseWriter, r *http.Request) {
	lat, lon, name := locationQuery(r)
	loc, err := ResolveLocationFor(lat, lon, name)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := buildGlanceResponse(r.Context(), loc, NoProgress)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Log(r.Context(), LevelTrace, "encode glance response", "err", err)
	}
}

// buildGlanceResponse fans out the rain providers + Open-Meteo for `loc` and
// returns the unified payload consumed by /api/v1/glance, /, and the CLI
// root command. Returns an error only when every upstream failed; partial
// results (e.g. radar present but alarm down) are passed through with the
// missing fields nil/zero so the renderer can degrade gracefully.
func buildGlanceResponse(ctx context.Context, loc Location, prog Progress) (*glanceAPIResponse, error) {
	var (
		alarm, radar       *Forecast
		alarmErr, radarErr error
		meteo              *OpenMeteoData
		meteoErr           error
		wg                 sync.WaitGroup
	)
	// Open-Meteo is the third unit of work alongside the two rain fetches.
	prog.AddTotal(1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		alarm, radar, alarmErr, radarErr = fetchRain(ctx, loc.Latitude, loc.Longitude, prog)
	}()
	go func() {
		defer wg.Done()
		defer prog.Inc(1)
		now := time.Now()
		meteo, meteoErr = GetOpenMeteoRange(loc.Latitude, loc.Longitude, now, now.Add(24*time.Hour))
	}()
	wg.Wait()

	if alarm == nil && radar == nil && meteo == nil {
		return nil, errors.Join(alarmErr, radarErr, meteoErr)
	}

	resp := &glanceAPIResponse{
		Location:   loc,
		Buienalarm: alarm,
		Buineradar: radar,
	}

	if meteo != nil && len(meteo.Hourly) > 0 {
		now := time.Now()
		end := now.Add(2 * time.Hour)
		resp.Temperature = glancePair{
			Now: interpolateInt(meteo.Hourly, now, func(h HourlyForecast) float64 { return h.Temperature }),
			End: interpolateInt(meteo.Hourly, end, func(h HourlyForecast) float64 { return h.Temperature }),
		}
		resp.FeelsLike = glancePair{
			Now: interpolateInt(meteo.Hourly, now, func(h HourlyForecast) float64 { return h.ApparentTemperature }),
			End: interpolateInt(meteo.Hourly, end, func(h HourlyForecast) float64 { return h.ApparentTemperature }),
		}
		resp.UVIndex = glancePair{
			Now: interpolateInt(meteo.Hourly, now, func(h HourlyForecast) float64 { return h.UVIndex }),
			End: interpolateInt(meteo.Hourly, end, func(h HourlyForecast) float64 { return h.UVIndex }),
		}
		resp.Wind = glanceWindPair{
			Now: glanceWind{
				SpeedKmh:     interpolateInt(meteo.Hourly, now, func(h HourlyForecast) float64 { return h.WindSpeed }),
				DirectionDeg: interpolateDirectionInt(meteo.Hourly, now, func(h HourlyForecast) float64 { return h.WindDirection }),
			},
			End: glanceWind{
				SpeedKmh:     interpolateInt(meteo.Hourly, end, func(h HourlyForecast) float64 { return h.WindSpeed }),
				DirectionDeg: interpolateDirectionInt(meteo.Hourly, end, func(h HourlyForecast) float64 { return h.WindDirection }),
			},
		}
		resp.Condition = wmoCondition(weatherCodeAt(meteo.Hourly, now))
		resp.Sun = sunEventsInWindow(meteo.Daily, now, end)
		resp.Sunset = nextSunset(meteo.Daily, now)
	}
	return resp, nil
}

// nextSunset returns the first sunset at or after `now` (RFC3339, local zone),
// falling back to the earliest known sunset so the widget always has a time to
// show. Empty when no daily data carries a sunset.
func nextSunset(daily []DailyForecast, now time.Time) string {
	fallback := ""
	for _, d := range daily {
		if d.Sunset.IsZero() {
			continue
		}
		if fallback == "" {
			fallback = d.Sunset.Format(time.RFC3339)
		}
		if d.Sunset.After(now) {
			return d.Sunset.Format(time.RFC3339)
		}
	}
	return fallback
}

// IsDry returns true when both providers stay below the dry threshold across
// every available data point. The chart is suppressed in this state and the
// hero ("23° → 21° · clear") takes its place.
func (g *glanceAPIResponse) IsDry() bool {
	if g == nil {
		return true
	}
	peak := 0.0
	for _, p := range forecastPoints(g.Buienalarm) {
		if p.Value > peak {
			peak = p.Value
		}
	}
	for _, p := range forecastPoints(g.Buineradar) {
		if p.Value > peak {
			peak = p.Value
		}
	}
	return peak < DryThresholdMmH
}

func forecastPoints(f *Forecast) []ForecastDataPoint {
	if f == nil {
		return nil
	}
	return f.Data
}

// interpolateInt linearly interpolates a scalar selected by `get` over the
// hourly forecast to instant t, rounding to a whole integer. Falls back to
// the nearest endpoint if t lies outside [hourly[0].Time, hourly[-1].Time].
func interpolateInt(hourly []HourlyForecast, t time.Time, get func(HourlyForecast) float64) int {
	if len(hourly) == 0 {
		return 0
	}
	if !t.After(hourly[0].Time) {
		return int(round(get(hourly[0])))
	}
	if !t.Before(hourly[len(hourly)-1].Time) {
		return int(round(get(hourly[len(hourly)-1])))
	}
	for i := 0; i < len(hourly)-1; i++ {
		a, b := hourly[i], hourly[i+1]
		if !t.Before(a.Time) && t.Before(b.Time) {
			span := b.Time.Sub(a.Time).Seconds()
			frac := t.Sub(a.Time).Seconds() / span
			va, vb := get(a), get(b)
			return int(round(va + frac*(vb-va)))
		}
	}
	return int(round(get(hourly[len(hourly)-1])))
}

// interpolateDirectionInt interpolates a compass direction (degrees) by
// taking the shorter arc between the two bracketing hours. Avoids the
// 359° → 1° wrap-around problem.
func interpolateDirectionInt(hourly []HourlyForecast, t time.Time, get func(HourlyForecast) float64) int {
	if len(hourly) == 0 {
		return 0
	}
	if !t.After(hourly[0].Time) {
		return int(round(normalizeDeg(get(hourly[0]))))
	}
	if !t.Before(hourly[len(hourly)-1].Time) {
		return int(round(normalizeDeg(get(hourly[len(hourly)-1]))))
	}
	for i := 0; i < len(hourly)-1; i++ {
		a, b := hourly[i], hourly[i+1]
		if !t.Before(a.Time) && t.Before(b.Time) {
			span := b.Time.Sub(a.Time).Seconds()
			frac := t.Sub(a.Time).Seconds() / span
			va := normalizeDeg(get(a))
			vb := normalizeDeg(get(b))
			diff := vb - va
			if diff > 180 {
				diff -= 360
			} else if diff < -180 {
				diff += 360
			}
			return int(round(normalizeDeg(va + frac*diff)))
		}
	}
	return int(round(normalizeDeg(get(hourly[len(hourly)-1]))))
}

// sunEventsInWindow returns sunrise and sunset events whose timestamp
// falls in (start, end]. Used by the widget to draw a vertical marker
// when the sun rises or sets within the visible 2-hour forecast.
func sunEventsInWindow(daily []DailyForecast, start, end time.Time) []sunEvent {
	out := make([]sunEvent, 0, 2)
	for _, d := range daily {
		if d.Sunrise.After(start) && !d.Sunrise.After(end) {
			out = append(out, sunEvent{Kind: "sunrise", Time: d.Sunrise.Format(time.RFC3339)})
		}
		if d.Sunset.After(start) && !d.Sunset.After(end) {
			out = append(out, sunEvent{Kind: "sunset", Time: d.Sunset.Format(time.RFC3339)})
		}
	}
	return out
}

func normalizeDeg(d float64) float64 {
	for d < 0 {
		d += 360
	}
	for d >= 360 {
		d -= 360
	}
	return d
}

// weatherCodeAt returns the WMO weather code for the hour bucket
// containing t. Falls back to the nearest bucket if t is outside the
// range.
func weatherCodeAt(hourly []HourlyForecast, t time.Time) int {
	if len(hourly) == 0 {
		return 0
	}
	if !t.After(hourly[0].Time) {
		return hourly[0].WeatherCode
	}
	if !t.Before(hourly[len(hourly)-1].Time) {
		return hourly[len(hourly)-1].WeatherCode
	}
	for i := 0; i < len(hourly)-1; i++ {
		if !t.Before(hourly[i].Time) && t.Before(hourly[i+1].Time) {
			return hourly[i].WeatherCode
		}
	}
	return hourly[len(hourly)-1].WeatherCode
}

// wmoCondition collapses the full WMO weather-code space into a small
// enum the widget renders. Values match the planned condition tokens.
func wmoCondition(code int) string {
	switch {
	case code == 0:
		return "clear"
	case code == 1 || code == 2:
		return "partly_cloudy"
	case code == 3:
		return "overcast"
	case code == 45 || code == 48:
		return "fog"
	case code >= 51 && code <= 57:
		return "drizzle"
	case (code >= 61 && code <= 67) || (code >= 80 && code <= 82):
		return "rain"
	case (code >= 71 && code <= 77) || code == 85 || code == 86:
		return "snow"
	case code == 95 || code == 96 || code == 99:
		return "thunderstorm"
	default:
		return "clear"
	}
}

// round is math.Round inlined to avoid pulling math just for this.
func round(v float64) float64 {
	if v >= 0 {
		return float64(int(v + 0.5))
	}
	return float64(int(v - 0.5))
}
