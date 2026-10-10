package cmd

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

type rideWindowRow struct {
	Start      string
	End        string
	RainChance int
	Wind       int
	Gusts      int
	Feels      int
	UV         int
}

func handleOutlookJSON(w http.ResponseWriter, r *http.Request) {
	period := r.URL.Query().Get("period")
	if period == "" {
		period = outlookToday
	}
	if _, _, err := outlookBounds(period, time.Now()); err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	location, _, err := resolveRequestLocation(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	result, err := fetchDetailedOutlook(location.Latitude, location.Longitude, period, time.Now())
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	response := struct {
		Location Location `json:"location"`
		detailedOutlook
	}{location, result}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Log(r.Context(), LevelTrace, "encode detailed outlook", "err", err)
	}
}

func handleOutlook(w http.ResponseWriter, r *http.Request) {
	period, title := outlookToday, "Today"
	switch r.URL.Path {
	case "/tomorrow":
		period, title = outlookTomorrow, "Tomorrow"
	case "/weekend":
		period, title = outlookWeekend, "Next weekend"
	}
	location, name, err := resolveRequestLocation(r)
	if err != nil {
		http.Error(w, "could not resolve location: "+err.Error(), http.StatusBadRequest)
		return
	}
	page := hourlyPageData{Title: title, Period: period, Location: location, Q: locQuery(location, name), NameInput: name}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := hourlyHeadTmpl.Execute(w, page); err != nil {
		slog.Debug("render outlook head", "err", err)
		return
	}
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	progress := NoProgress
	if flusher != nil {
		progress = NewHTTPProgress(w, flusher)
	}
	progress.AddTotal(1)
	result, err := fetchDetailedOutlook(location.Latitude, location.Longitude, period, time.Now())
	progress.Inc(1)
	progress.Finish()
	page.Now = time.Now().In(locationZone(location.Latitude, location.Longitude)).Format("15:04:05")
	if err != nil {
		page.Note = "Unable to fetch the detailed forecast right now."
		slog.Warn("fetch detailed outlook", "period", period, "err", err)
	} else {
		populateOutlookPage(&page, result)
	}
	if err := hourlyBodyTmpl.Execute(w, page); err != nil {
		slog.Debug("render outlook body", "err", err)
	}
}

func populateOutlookPage(page *hourlyPageData, result detailedOutlook) {
	page.StartLabel = result.Start.Format("Mon 2 Jan")
	page.EndLabel = result.End.Add(-time.Nanosecond).Format("Mon 2 Jan")
	populateHourlyPage(page, result.Hours)
	for _, ride := range result.Rides {
		page.Rides = append(page.Rides, rideWindowRow{Start: ride.Start.Format("Mon 15:04"), End: ride.End.Format("15:04"), RainChance: ride.RainChance, Wind: int(round(ride.Wind)), Gusts: int(round(ride.Gusts)), Feels: int(round(ride.Feels)), UV: int(round(ride.UV))})
	}
}

func populateHourlyPage(page *hourlyPageData, hours []HourlyForecast) {
	var temps, feels, rain []ForecastDataPoint
	lastDay := ""
	for _, hour := range hours {
		day := hour.Time.Format("2006-01-02")
		label := hour.Time.Format("15:04")
		if day != lastDay {
			label = hour.Time.Format("Mon 15:04")
		}
		wind, gusts, uv := int(round(hour.WindSpeed)), int(round(hour.WindGusts)), int(round(hour.UVIndex))
		page.Rows = append(page.Rows, hourlyRow{
			Time: label, NewDay: day != lastDay && lastDay != "", Temp: int(round(hour.Temperature)), Feels: int(round(hour.ApparentTemperature)), Precip: formatPrecip(hour.Precipitation), PrecipPct: hour.PrecipitationProbability,
			WindArrow: windArrowFor(int(round(hour.WindDirection))), WindKmh: wind, WindClass: windClassFor(wind), GustsKmh: gusts, GustClass: windClassFor(gusts), UV: uv, UVClass: uvClassFor(uv), Condition: conditionHumanLabel(wmoCondition(hour.WeatherCode)), ConditionIcon: conditionIcon(wmoCondition(hour.WeatherCode)),
		})
		lastDay = day
		temps = append(temps, ForecastDataPoint{Time: hour.Time, Value: hour.Temperature})
		feels = append(feels, ForecastDataPoint{Time: hour.Time, Value: hour.ApparentTemperature})
		rain = append(rain, ForecastDataPoint{Time: hour.Time, Value: hour.Precipitation})
	}
	if len(temps) >= 2 {
		page.TempChartSVG = RenderLineChartSVG([]SVGSeries{{Name: "Temp", Color: tempColor, Data: temps}, {Name: "Feels", Color: feelsColor, Data: feels}}, SVGOpts{YUnit: "°C", XTimeFormat: "Mon 15h"})
		page.PrecipChartSVG = RenderLineChartSVG([]SVGSeries{{Name: "Precip", Color: buienalarmColor, Data: rain}}, SVGOpts{YUnit: "mm", XTimeFormat: "Mon 15h", MinYHi: 1, FillArea: true})
	}
}

func conditionIcon(condition string) string {
	switch condition {
	case "partly_cloudy":
		return "🌤️"
	case "overcast":
		return "☁️"
	case "fog":
		return "🌫️"
	case "drizzle":
		return "🌦️"
	case "rain":
		return "🌧️"
	case "snow":
		return "🌨️"
	case "thunderstorm":
		return "⛈️"
	default:
		return "☀️"
	}
}
