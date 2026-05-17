package cmd

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// glanceAPIResponse is the single-fetch payload consumed by the Android
// widget. Combines the precipitation nowcast (from Buienalarm/Buienradar)
// with a snapshot of temperature + weather condition from Open-Meteo so
// the widget only needs one HTTP round-trip per refresh.
type glanceAPIResponse struct {
	Location         Location       `json:"location"`
	Buienalarm       *Forecast      `json:"buienalarm"`
	Buineradar       *Forecast      `json:"buineradar"`
	Temperature      glancePair     `json:"temperature"`                      // °C
	FeelsLike        glancePair     `json:"feels_like"`                       // °C (apparent_temperature)
	Wind             glanceWindPair `json:"wind"`                             // km/h + degrees-from-N
	UVIndex          glancePair     `json:"uv_index"`                         // integer 0..11+
	PrecipProb       glancePair     `json:"precipitation_probability"`        // 0..100 (%)
	PrecipProbHourly []probPoint    `json:"precipitation_probability_hourly"` // hourly array spanning [now-1h, now+3h]
	Condition        string         `json:"condition"`
	Sun              []sunEvent     `json:"sun"` // events within [now, now+2h], empty if none
}

type probPoint struct {
	Time  string `json:"time"`
	Value int    `json:"value"`
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

	// Fan out: rain providers + Open-Meteo in parallel. Open-Meteo covers
	// today + tomorrow so a +2h window crossing midnight still resolves.
	var (
		alarm, radar       *Forecast
		alarmErr, radarErr error
		meteo              *OpenMeteoData
		meteoErr           error
		wg                 sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		alarm, radar, alarmErr, radarErr = fetchRain(r.Context(), loc.Latitude, loc.Longitude, NoProgress)
	}()
	go func() {
		defer wg.Done()
		now := time.Now()
		meteo, meteoErr = GetOpenMeteoRange(loc.Latitude, loc.Longitude, now, now.Add(24*time.Hour))
	}()
	wg.Wait()

	if alarm == nil && radar == nil && meteo == nil {
		writeJSONError(w, http.StatusBadGateway, errors.Join(alarmErr, radarErr, meteoErr))
		return
	}

	resp := glanceAPIResponse{
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
		resp.PrecipProb = glancePair{
			Now: interpolateInt(meteo.Hourly, now, func(h HourlyForecast) float64 { return float64(h.PrecipitationProbability) }),
			End: interpolateInt(meteo.Hourly, end, func(h HourlyForecast) float64 { return float64(h.PrecipitationProbability) }),
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
		resp.PrecipProbHourly = probabilityWindow(meteo.Hourly, now.Add(-1*time.Hour), end.Add(time.Hour))
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Log(r.Context(), LevelTrace, "encode glance response", "err", err)
	}
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

// probabilityWindow returns hourly precipitation_probability values whose
// time bucket overlaps the window [from, to]. The widget paints these as
// background tint behind the rain lines so the user reads "rain chance"
// alongside the actual rain forecast.
func probabilityWindow(hourly []HourlyForecast, from, to time.Time) []probPoint {
	out := make([]probPoint, 0, 8)
	for _, h := range hourly {
		// The hourly value covers the [time, time+1h) interval.
		end := h.Time.Add(time.Hour)
		if end.Before(from) || h.Time.After(to) {
			continue
		}
		out = append(out, probPoint{Time: h.Time.Format(time.RFC3339), Value: h.PrecipitationProbability})
	}
	return out
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
