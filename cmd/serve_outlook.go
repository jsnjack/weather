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

// rideNotes is the one line shown instead of the ride table when a period has
// no ride window.
var rideNotes = map[string]string{
	outlookToday:    "No ride window left today",
	outlookTomorrow: "No ride window tomorrow",
	outlookWeekend:  "No ride window this weekend",
}

func populateOutlookPage(page *hourlyPageData, result detailedOutlook) {
	populateHourlyPage(page, result.Hours, result.days)
	for _, ride := range result.Rides {
		page.Rides = append(page.Rides, rideWindowRow{Start: ride.Start.Format("Mon 15:04"), End: ride.End.Format("15:04"), RainChance: ride.RainChance, Wind: int(round(ride.Wind)), Gusts: int(round(ride.Gusts)), Feels: int(round(ride.Feels)), UV: int(round(ride.UV))})
	}
	if len(page.Rides) == 0 {
		page.RideNote = rideNotes[result.Period]
	}
}

// populateHourlyPage fills the hourly table and charts. days gives the sun
// times for night icons; the rain chart is left out when every hour is dry.
func populateHourlyPage(page *hourlyPageData, hours []HourlyForecast, days []DailyForecast) {
	if len(hours) > 0 {
		page.StartLabel = hours[0].Time.Format("Mon 2 Jan 15:04")
		page.EndLabel = hours[len(hours)-1].Time.Format("Mon 15:04")
	}
	var temps, feels, rain []ForecastDataPoint
	wet := false
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
			WindArrow: windArrowFor(int(round(hour.WindDirection))), WindKmh: wind, WindClass: windClassFor(wind), GustsKmh: gusts, GustClass: windClassFor(gusts), UV: uv, UVClass: uvClassFor(uv), Condition: conditionHumanLabel(wmoCondition(hour.WeatherCode)), ConditionIcon: conditionIcon(wmoCondition(hour.WeatherCode), nightHour(days, hour.Time)),
		})
		wet = wet || hour.Precipitation > 0
		lastDay = day
		temps = append(temps, ForecastDataPoint{Time: hour.Time, Value: hour.Temperature})
		feels = append(feels, ForecastDataPoint{Time: hour.Time, Value: hour.ApparentTemperature})
		rain = append(rain, ForecastDataPoint{Time: hour.Time, Value: hour.Precipitation})
	}
	if len(temps) >= 2 {
		page.TempChartSVG = RenderLineChartSVG([]SVGSeries{{Name: "Temp", Color: tempColor, Data: temps}, {Name: "Feels", Color: feelsColor, Data: feels}}, SVGOpts{YUnit: "°C", XTimeFormat: "Mon 15h"})
	}
	if len(rain) >= 2 && wet {
		page.PrecipChartSVG = RenderLineChartSVG([]SVGSeries{{Name: "Precip", Color: buienalarmColor, Data: rain}}, SVGOpts{YUnit: "mm", XTimeFormat: "Mon 15h", MinYHi: 1, FillArea: true})
	}
}
