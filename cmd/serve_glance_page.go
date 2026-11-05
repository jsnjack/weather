package cmd

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// glanceForecastDays is today plus the thirteen rows of the daily band.
	glanceForecastDays = 14
	glanceNowcastStep  = 5 * time.Minute
	glanceNowcastRowPx = 12
	glanceHourRowPx    = 30
	// glanceWetChance and glanceWetMM turn the rain cell blue. Open-Meteo's
	// daily chance can stay under 30% on a 17 mm day, so the amount counts too.
	glanceWetChance = 40
	glanceWetMM     = 1.0
	// glanceEventGapPx keeps the 2-hour band's event labels apart.
	glanceEventGapPx = 14
	// glanceEventMinPx keeps an event label at the band's top edge inside it.
	glanceEventMinPx = 6
	// glanceTickClearance drops a quarter-hour tick this close to the end tick.
	glanceTickClearance = 10 * time.Minute
	// glanceScaleMargin keeps the coldest and warmest values this fraction of
	// the °C track away from its edges, leaving room for their labels.
	glanceScaleMargin = 0.18
	glanceScaleMinPad = 2.0
	// glanceScaleEdge drops °C ticks this close to the track's ends, where
	// their labels would run into the neighbouring columns.
	glanceScaleEdge = 6.0
	glanceDateKey   = "2006-01-02"
)

// glancePageData drives the /glance templates. The head only needs the
// location fields; the rest is filled after the upstream fetches.
type glancePageData struct {
	Location        Location
	Q               template.URL
	NameInput       string
	BuienalarmColor string
	BuineradarColor string
	Timezone        string
	Date            string
	Updated         string
	Refreshed       string
	Readouts        *glanceReadouts
	Nowcast         glanceNowcastBand
	Today           glanceHourBand
	Days            glanceDayBand
	Scale           glanceTempScale
}

type glanceReadouts struct {
	Temp, Feels   int
	Wind, WindEnd windView
	UV, UVEnd     microView
	Condition     string
	ConditionIcon string
	By            string // clock time of the +2h values
}

// glanceMark is a label placed along a scale by an inline style.
type glanceMark struct {
	Label string
	Style template.CSS
}

// glanceColumns heads the hourly and daily rows; the temperature column
// carries the shared °C scale.
type glanceColumns struct {
	Name  string
	Temp  string
	Ticks []glanceMark
}

type glanceTick struct {
	Label string
	Top   int
}

type glanceProvider struct {
	Name   string
	Color  string
	points []ForecastDataPoint
}

type glanceEvent struct {
	Text  string
	Color string // provider dot; empty for events of both providers
	Top   int    // the event's time on the band
	Shift int    // label offset that keeps labels apart
	at    time.Time
}

type glanceNowcastBand struct {
	Message   string
	Note      string
	Dry       bool
	End       string
	HeightPx  int
	Providers []glanceProvider
	Scale     []glanceMark
	Ticks     []glanceTick
	Events    []glanceEvent
	PlotSVG   template.HTML
	Summary   string
	end       time.Time
}

// glanceRain is the rain cell of the hourly and daily rows: the chance in
// bold, the amount or "dry" under it, in the water colour from 40% or 1 mm.
type glanceRain struct {
	Chance int
	Amount string
	Wet    bool
}

type glanceTempLabel struct {
	Value int
	Feels bool // drawn in the feels-like colour
	Style template.CSS
}

type glanceTempTrack struct {
	Text      string
	LinkStyle template.CSS
	DotStyle  template.CSS
	RingStyle template.CSS
	BarStyle  template.CSS
	Labels    []glanceTempLabel
}

type glanceRide struct {
	Hours      string
	RainChance int
	Detail     string
}

type glanceSpan struct{ Top, Height int }

type glanceSunMark struct {
	Top      int
	LabelTop int
	Glyph    string
	Time     string
}

type glanceHourRow struct {
	Time          string
	Condition     string
	ConditionIcon string
	Rain          glanceRain
	Temp          glanceTempTrack
	Wind          windView
	Gust          microView
}

type glanceHourBand struct {
	Cols          glanceColumns
	Note          string
	Summary       string
	Rides         []glanceRide
	RideNote      string
	TomorrowRides []glanceRide
	Rows          []glanceHourRow
	TraceSVG      template.HTML
	Night         []glanceSpan
	Sun           []glanceSunMark
}

type glanceDayRow struct {
	Week          int // ISO week shown above the row; zero for none
	Label         string
	Weekend       bool
	Condition     string
	ConditionIcon string
	Rain          glanceRain
	Temp          glanceTempTrack
	Wind          windView
	Gust          microView
	Rides         []glanceRide
	date          time.Time
}

type glanceDayBand struct {
	Cols glanceColumns
	Note string
	Rows []glanceDayRow
}

// glanceTempScale is the °C axis shared by the hourly and daily rows.
type glanceTempScale struct {
	Ticks  []glanceMark
	Lines  template.CSS
	lo, hi float64
}

type glanceSources struct {
	glance    *glanceAPIResponse
	glanceErr error
	hourly    *OpenMeteoData
	hourlyErr error
	daily     []DailyAggregate
	dailyErr  error
}

func handleGlance(w http.ResponseWriter, r *http.Request) {
	loc, name, err := resolveRequestLocation(r)
	if err != nil {
		http.Error(w, "could not resolve location: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)

	page := glancePageData{Location: loc, Q: locQuery(loc, name), NameInput: name, BuienalarmColor: buienalarmColor, BuineradarColor: buineradarColor}
	if err := glanceHeadTmpl.Execute(w, page); err != nil {
		slog.Debug("template execute", "tmpl", "glanceHead", "err", err)
		return
	}
	if flusher != nil {
		flusher.Flush()
	}

	prog := NoProgress
	if flusher != nil {
		prog = NewHTTPProgress(w, flusher)
	}
	sources := fetchGlanceSources(r.Context(), loc, prog)
	prog.Finish()
	populateGlancePage(&page, sources, time.Now())
	if err := glanceBodyTmpl.Execute(w, page); err != nil {
		slog.Debug("template execute", "tmpl", "glanceBody", "err", err)
	}
}

// fetchGlanceSources loads the nowcast, the hourly and the daily forecast in
// parallel. The hourly response is shared with the nowcast snapshot through
// the Open-Meteo cache, so it is fetched once.
func fetchGlanceSources(ctx context.Context, loc Location, prog Progress) glanceSources {
	var src glanceSources
	var wg sync.WaitGroup
	prog.AddTotal(2)
	wg.Go(func() { src.glance, src.glanceErr = buildGlanceResponse(ctx, loc, prog) })
	wg.Go(func() {
		defer prog.Inc(1)
		src.hourly, src.hourlyErr = getLocationForecast(loc.Latitude, loc.Longitude)
	})
	wg.Go(func() {
		defer prog.Inc(1)
		src.daily, src.dailyErr = GetOpenMeteoDailyRange(loc.Latitude, loc.Longitude, glanceForecastDays)
	})
	wg.Wait()
	if src.glanceErr != nil {
		slog.Warn("glance: nowcast unavailable", "err", src.glanceErr)
	}
	if src.hourlyErr != nil {
		slog.Warn("glance: hourly forecast unavailable", "err", src.hourlyErr)
	}
	if src.dailyErr != nil {
		slog.Warn("glance: daily forecast unavailable", "err", src.dailyErr)
	}
	return src
}

// populateGlancePage fills every band from whatever sources arrived; each
// band degrades to a note on its own.
func populateGlancePage(page *glancePageData, src glanceSources, now time.Time) {
	zone := nowcastZone(page.Location.Latitude, page.Location.Longitude)
	if src.hourly != nil && len(src.hourly.Hourly) > 0 {
		zone = src.hourly.Hourly[0].Time.Location()
	}
	now = now.In(zone)
	page.Timezone = zone.String()
	page.Date = now.Format("Mon 2 Jan")
	page.Updated = now.Format("15:04")
	page.Refreshed = now.Format("15:04:05")
	page.Readouts = glanceReadoutsFrom(src.glance, now)
	page.Nowcast = buildGlanceNowcast(src.glance)

	var hours []HourlyForecast
	var todayRides, tomorrowRides []rideWindow
	var sunrise, sunset time.Time
	today, todayErr := buildDetailedOutlook(src.hourly, outlookToday, now)
	if todayErr != nil {
		page.Today.Note = "Unable to fetch the hourly forecast right now."
	} else {
		// The hourly rows continue where the 5-minute rows end.
		var from time.Time
		if !page.Nowcast.end.IsZero() {
			from = localHour(page.Nowcast.end)
		}
		for _, hour := range today.Hours {
			if !hour.Time.Before(from) {
				hours = append(hours, hour)
			}
		}
		if len(hours) == 0 {
			page.Today.Note = "The 2-hour nowcast covers the rest of today."
		}
		todayRides = today.Rides
		if tomorrow, err := buildDetailedOutlook(src.hourly, outlookTomorrow, now); err == nil {
			tomorrowRides = tomorrow.Rides
		}
		sunrise, sunset = glanceSunTimes(src.hourly.Daily, now)
	}

	var days []DailyAggregate
	todayKey := now.Format(glanceDateKey)
	if src.dailyErr != nil || len(src.daily) == 0 {
		page.Days.Note = "Unable to fetch the daily forecast right now."
	}
	for _, day := range src.daily {
		switch key := day.Date.Format(glanceDateKey); {
		case key == todayKey:
			page.Today.Summary = glanceDaySummary(day)
		case key > todayKey && len(days) < glanceForecastDays-1:
			days = append(days, day)
		}
	}

	var temps []float64
	for _, hour := range hours {
		temps = append(temps, hour.Temperature, hour.ApparentTemperature)
	}
	for _, day := range days {
		temps = append(temps, day.TempMin, day.TempMax)
	}
	page.Scale = newGlanceTempScale(temps)
	page.Today.Cols = glanceColumns{Name: "Time", Temp: "Temperature and feels like in °C", Ticks: page.Scale.Ticks}
	page.Days.Cols = glanceColumns{Name: "Day", Temp: "Low and high temperature in °C", Ticks: page.Scale.Ticks}
	page.Today.Rows = glanceHourRows(hours, src.hourly.Daily, page.Scale)
	page.Today.TraceSVG = glanceTraceSVG(hours, page.Scale)
	page.Today.Night, page.Today.Sun = glanceDaylight(hours, sunrise, sunset)
	page.Days.Rows = glanceDayRows(days, page.Scale)

	if todayErr != nil {
		return
	}
	if len(todayRides) > 0 {
		page.Today.Rides = glanceRides(todayRides)
		return
	}
	if len(tomorrowRides) == 0 {
		page.Today.RideNote = "No dry 2-hour ride window in daylight for the rest of today or tomorrow"
		return
	}
	page.Today.RideNote = "No dry 2-hour ride window left in daylight"
	tomorrowKey := now.AddDate(0, 0, 1).Format(glanceDateKey)
	if len(page.Days.Rows) > 0 && page.Days.Rows[0].date.Format(glanceDateKey) == tomorrowKey {
		page.Days.Rows[0].Rides = glanceRides(tomorrowRides)
	} else {
		page.Today.TomorrowRides = glanceRides(tomorrowRides)
	}
}

func glanceReadoutsFrom(g *glanceAPIResponse, now time.Time) *glanceReadouts {
	// The condition is only set when the Open-Meteo snapshot arrived.
	if g == nil || g.Condition == "" {
		return nil
	}
	return &glanceReadouts{
		Temp:          g.Temperature.Now,
		Feels:         g.FeelsLike.Now,
		Wind:          makeWindView(g.Wind.Now),
		WindEnd:       makeWindView(g.Wind.End),
		UV:            makeUVView(g.UVIndex.Now),
		UVEnd:         makeUVView(g.UVIndex.End),
		Condition:     conditionHumanLabel(g.Condition),
		ConditionIcon: conditionIcon(g.Condition),
		By:            now.Add(2 * time.Hour).Format("15:04"),
	}
}

// buildGlanceNowcast draws both providers down the time axis, capped at the
// Buienalarm horizon like the rain chart, with labelled rain events. It uses
// only the radar nowcast, never Open-Meteo's precipitation probability.
func buildGlanceNowcast(g *glanceAPIResponse) glanceNowcastBand {
	var band glanceNowcastBand
	var alarm, radar []ForecastDataPoint
	if g != nil {
		alarm, radar = forecastPoints(g.Buienalarm), forecastPoints(g.Buineradar)
		if g.Buienalarm != nil {
			band.Message = g.Buienalarm.Desc
		}
	}
	if n := len(alarm); n > 0 {
		capped := radar[:0:0]
		for _, point := range radar {
			if !point.Time.After(alarm[n-1].Time) {
				capped = append(capped, point)
			}
		}
		radar = capped
	}
	if len(alarm) > 0 {
		band.Providers = append(band.Providers, glanceProvider{Name: "Buienalarm", Color: buienalarmColor, points: alarm})
	}
	if len(radar) > 0 {
		band.Providers = append(band.Providers, glanceProvider{Name: "Buienradar", Color: buineradarColor, points: radar})
	}
	if len(band.Providers) == 0 {
		band.Note = "The rain nowcast is unavailable right now."
		if band.Message == "" {
			band.Message = "Rain nowcast unavailable"
		}
		return band
	}

	start, end := band.Providers[0].points[0].Time, band.Providers[0].points[len(band.Providers[0].points)-1].Time
	peak := 0.0
	var firstWet, lastWet time.Time
	for _, provider := range band.Providers {
		start = minTime(start, provider.points[0].Time)
		end = maxTime(end, provider.points[len(provider.points)-1].Time)
		for _, point := range provider.points {
			peak = max(peak, point.Value)
			if point.Value < DryThresholdMmH {
				continue
			}
			if firstWet.IsZero() || point.Time.Before(firstWet) {
				firstWet = point.Time
			}
			lastWet = maxTime(lastWet, point.Time)
		}
	}
	band.end = end
	band.End = end.Format("15:04")
	band.Dry = firstWet.IsZero()
	rainNow := !band.Dry && !firstWet.After(start)
	if band.Message == "" {
		switch {
		case band.Dry:
			band.Message = "No rain expected until " + band.End
		case rainNow:
			band.Message = "Rain now"
		default:
			band.Message = "Rain expected from " + firstWet.Format("15:04")
		}
	}
	if band.Dry {
		return band
	}

	rows := max(1, int(math.Round(end.Sub(start).Minutes()/glanceNowcastStep.Minutes())))
	band.HeightPx = rows * glanceNowcastRowPx
	y := func(at time.Time) float64 {
		return at.Sub(start).Minutes() / glanceNowcastStep.Minutes() * glanceNowcastRowPx
	}

	// MinYHi=1 like the rain chart, so drizzle does not fill the scale.
	top := max(peak, 1)
	step := niceStep(top, 4)
	top = math.Ceil(top/step-1e-9) * step
	var grid []float64
	for i := 0; float64(i)*step <= top+1e-9; i++ {
		value := float64(i) * step
		grid = append(grid, value)
		label := strconv.FormatFloat(value, 'f', -1, 64)
		if float64(i+1)*step > top+1e-9 {
			label += " " + PrecipitationForecast.Unit()
		}
		band.Scale = append(band.Scale, glanceMark{Label: label, Style: glanceCSS("left:%.2f%%", value/top*100)})
	}

	quarter := start.Truncate(15 * time.Minute)
	if quarter.Before(start) {
		quarter = quarter.Add(15 * time.Minute)
	}
	for ; end.Sub(quarter) >= glanceTickClearance; quarter = quarter.Add(15 * time.Minute) {
		band.Ticks = append(band.Ticks, glanceTick{Label: quarter.Format("15:04"), Top: int(math.Round(y(quarter)))})
	}

	summary := make([]string, 0, 4)
	if rainNow {
		band.Events = append(band.Events, glanceEvent{Text: "Rain now", at: start})
		summary = append(summary, "rain now")
	} else {
		band.Events = append(band.Events, glanceEvent{Text: "Rain from " + firstWet.Format("15:04"), at: firstWet})
		summary = append(summary, "rain from "+firstWet.Format("15:04"))
	}
	for _, provider := range band.Providers {
		best := provider.points[0]
		for _, point := range provider.points {
			if point.Value > best.Value {
				best = point
			}
		}
		if best.Value < DryThresholdMmH {
			continue
		}
		band.Events = append(band.Events, glanceEvent{Text: fmt.Sprintf("Peak %.1f %s", best.Value, PrecipitationForecast.Unit()), Color: provider.Color, at: best.Time})
		summary = append(summary, fmt.Sprintf("%s peak %.1f %s at %s", provider.Name, best.Value, PrecipitationForecast.Unit(), best.Time.Format("15:04")))
	}
	var dryAgain time.Time
	for _, provider := range band.Providers {
		for _, point := range provider.points {
			if point.Time.After(lastWet) && (dryAgain.IsZero() || point.Time.Before(dryAgain)) {
				dryAgain = point.Time
			}
		}
	}
	if dryAgain.IsZero() {
		summary = append(summary, "rain continues past "+band.End)
	} else {
		band.Events = append(band.Events, glanceEvent{Text: "Dry again ~" + dryAgain.Format("15:04"), at: dryAgain})
		summary = append(summary, "dry again around "+dryAgain.Format("15:04"))
	}
	sort.SliceStable(band.Events, func(i, j int) bool { return band.Events[i].at.Before(band.Events[j].at) })
	previous := math.Inf(-1)
	for i := range band.Events {
		at := y(band.Events[i].at)
		label := max(at, previous+glanceEventGapPx, glanceEventMinPx)
		band.Events[i].Top = int(math.Round(at))
		band.Events[i].Shift = int(math.Round(label - at))
		previous = label
	}
	band.Summary = fmt.Sprintf("Rain in %s from %s to %s, drawn down the time axis: %s.",
		PrecipitationForecast.Unit(), start.Format("15:04"), band.End, strings.Join(summary, "; "))
	band.PlotSVG = glanceNowcastSVG(band.Providers, y, band.HeightPx, top, grid)
	return band
}

func glanceNowcastSVG(providers []glanceProvider, y func(time.Time) float64, height int, top float64, grid []float64) template.HTML {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="g-plot" viewBox="0 0 100 %d" preserveAspectRatio="none" aria-hidden="true" focusable="false">`, height)
	for _, value := range grid[1:] {
		x := value / top * 100
		fmt.Fprintf(&b, `<line x1="%.2f" y1="0" x2="%.2f" y2="%d"/>`, x, x, height)
	}
	// Buienradar first, so Buienalarm draws on top as in the rain chart.
	for i := len(providers) - 1; i >= 0; i-- {
		provider := providers[i]
		points := make([]string, 0, len(provider.points))
		for _, point := range provider.points {
			points = append(points, fmt.Sprintf("%.2f,%.2f", min(point.Value/top, 1)*100, y(point.Time)))
		}
		joined := strings.Join(points, " ")
		first, last := y(provider.points[0].Time), y(provider.points[len(provider.points)-1].Time)
		fmt.Fprintf(&b, `<polygon points="0,%.2f %s 0,%.2f" fill="%s" fill-opacity=".2"/>`, first, joined, last, provider.Color)
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="2" stroke-linejoin="round" vector-effect="non-scaling-stroke"/>`, joined, provider.Color)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// makeGlanceRain formats the rain cell. A likely shower with a trace amount
// reads as wet, and so does a large amount at a low chance.
func makeGlanceRain(mm float64, chance int) glanceRain {
	amount := "dry"
	if text := formatPrecip(mm); text != "" {
		amount = text + " mm"
	}
	return glanceRain{Chance: chance, Amount: amount, Wet: chance >= glanceWetChance || mm >= glanceWetMM}
}

func newGlanceTempScale(values []float64) glanceTempScale {
	if len(values) == 0 {
		return glanceTempScale{}
	}
	lo, hi := slices.Min(values), slices.Max(values)
	pad := max(glanceScaleMinPad, (hi-lo)*glanceScaleMargin/(1-2*glanceScaleMargin))
	scale := glanceTempScale{lo: lo - pad, hi: hi + pad}
	step := niceStep(scale.hi-scale.lo, 4)
	var lines []string
	for i := math.Ceil(scale.lo / step); i*step <= scale.hi; i++ {
		value := i * step
		if value == 0 {
			value = 0 // drops the sign of -0
		}
		if pos := scale.pos(value); pos < glanceScaleEdge || pos > 100-glanceScaleEdge {
			continue
		}
		x := fmt.Sprintf("%.2f%%", scale.pos(value))
		scale.Ticks = append(scale.Ticks, glanceMark{Label: strconv.FormatFloat(value, 'f', -1, 64) + "°", Style: template.CSS("left:" + x)})
		lines = append(lines, fmt.Sprintf("linear-gradient(90deg,transparent calc(%[1]s - .5px),var(--g-grid) calc(%[1]s - .5px),var(--g-grid) calc(%[1]s + .5px),transparent calc(%[1]s + .5px))", x))
	}
	scale.Lines = template.CSS("--g-lines:" + strings.Join(lines, ","))
	return scale
}

func (s glanceTempScale) pos(celsius float64) float64 {
	return (celsius - s.lo) / (s.hi - s.lo) * 100
}

// hourTrack places the temperature dot and the feels-like ring; the lower
// value is labelled on its left and the higher on its right.
func (s glanceTempScale) hourTrack(temp, feels float64) glanceTempTrack {
	t, f := int(round(temp)), int(round(feels))
	xt, xf := s.pos(temp), s.pos(feels)
	track := glanceTempTrack{
		Text:      fmt.Sprintf("%d°, feels like %d°", t, f),
		LinkStyle: glanceCSS("left:%.2f%%;width:%.2f%%", min(xt, xf), math.Abs(xt-xf)),
		DotStyle:  glanceCSS("left:%.2f%%", xt),
		RingStyle: glanceCSS("left:%.2f%%", xf),
	}
	lower, upper := glanceTempLabel{Value: f, Feels: true}, glanceTempLabel{Value: t}
	xl, xu := xf, xt
	if feels > temp {
		lower, upper, xl, xu = upper, lower, xt, xf
	}
	lower.Style = glanceCSS("right:calc(%.2f%% + 7px)", 100-xl)
	upper.Style = glanceCSS("left:calc(%.2f%% + 7px)", xu)
	track.Labels = []glanceTempLabel{lower, upper}
	return track
}

func (s glanceTempScale) dayTrack(low, high float64) glanceTempTrack {
	lo, hi := int(round(low)), int(round(high))
	xl, xh := s.pos(low), s.pos(high)
	return glanceTempTrack{
		Text:     fmt.Sprintf("low %d°, high %d°", lo, hi),
		BarStyle: glanceCSS("left:%.2f%%;width:%.2f%%", xl, xh-xl),
		Labels: []glanceTempLabel{
			{Value: lo, Feels: true, Style: glanceCSS("right:calc(%.2f%% + 4px)", 100-xl)},
			{Value: hi, Style: glanceCSS("left:calc(%.2f%% + 4px)", xh)},
		},
	}
}

func glanceHourRows(hours []HourlyForecast, days []DailyForecast, scale glanceTempScale) []glanceHourRow {
	rows := make([]glanceHourRow, 0, len(hours))
	for _, hour := range hours {
		condition := wmoCondition(hour.WeatherCode)
		rows = append(rows, glanceHourRow{
			Time:          hour.Time.Format("15:04"),
			Condition:     conditionHumanLabel(condition),
			ConditionIcon: glanceConditionIcon(condition, glanceNight(days, hour.Time)),
			Rain:          makeGlanceRain(hour.Precipitation, hour.PrecipitationProbability),
			Temp:          scale.hourTrack(hour.Temperature, hour.ApparentTemperature),
			Wind:          makeWindView(glanceWind{SpeedKmh: int(round(hour.WindSpeed)), DirectionDeg: int(round(hour.WindDirection))}),
			Gust:          glanceGust(hour.WindGusts),
		})
	}
	return rows
}

func glanceDayRows(days []DailyAggregate, scale glanceTempScale) []glanceDayRow {
	rows := make([]glanceDayRow, 0, len(days))
	for i, day := range days {
		row := glanceDayRow{
			Label:         day.Date.Format("Mon 2"),
			Weekend:       day.Date.Weekday() == time.Saturday || day.Date.Weekday() == time.Sunday,
			Condition:     conditionHumanLabel(day.Condition),
			ConditionIcon: conditionIcon(day.Condition),
			Rain:          makeGlanceRain(day.PrecipSum, day.PrecipProbMax),
			Temp:          scale.dayTrack(day.TempMin, day.TempMax),
			Wind:          makeWindView(glanceWind{SpeedKmh: int(round(day.WindMax)), DirectionDeg: int(round(day.WindDirDominant))}),
			Gust:          glanceGust(day.GustMax),
			date:          day.Date,
		}
		if i == 0 || day.Date.Weekday() == time.Monday {
			_, row.Week = day.Date.ISOWeek()
		}
		rows = append(rows, row)
	}
	return rows
}

func glanceGust(kmh float64) microView {
	gust := int(round(kmh))
	return microView{Value: gust, Class: windClassFor(gust)}
}

func glanceTraceSVG(hours []HourlyForecast, scale glanceTempScale) template.HTML {
	if len(hours) < 2 {
		return ""
	}
	temps, feels := make([]string, 0, len(hours)), make([]string, 0, len(hours))
	for i, hour := range hours {
		y := float64(i*glanceHourRowPx) + glanceHourRowPx/2.0
		temps = append(temps, fmt.Sprintf("%.2f,%.1f", scale.pos(hour.Temperature), y))
		feels = append(feels, fmt.Sprintf("%.2f,%.1f", scale.pos(hour.ApparentTemperature), y))
	}
	return template.HTML(fmt.Sprintf(`<svg class="g-trace" viewBox="0 0 100 %d" preserveAspectRatio="none" aria-hidden="true" focusable="false"><polyline class="g-tt" points="%s"/><polyline class="g-tf" points="%s"/></svg>`,
		len(hours)*glanceHourRowPx, strings.Join(temps, " "), strings.Join(feels, " ")))
}

// glanceNight reports whether most of the hour starting at t falls between
// that day's sunset and sunrise. Unknown sun times count as day.
func glanceNight(days []DailyForecast, t time.Time) bool {
	mid := t.Add(30 * time.Minute)
	for _, day := range days {
		if day.Sunrise.IsZero() || day.Sunset.IsZero() || day.Sunrise.Format(glanceDateKey) != mid.Format(glanceDateKey) {
			continue
		}
		return mid.Before(day.Sunrise) || !mid.Before(day.Sunset)
	}
	return false
}

// glanceConditionIcon swaps the sun for a moon in clear and partly cloudy
// hours after dark; the other conditions have no sun in their icon.
func glanceConditionIcon(condition string, night bool) string {
	icon := conditionIcon(condition)
	if !night {
		return icon
	}
	switch icon {
	case "☀️":
		return "🌙"
	case "🌤️":
		return "☁️"
	}
	return icon
}

func glanceSunTimes(days []DailyForecast, now time.Time) (time.Time, time.Time) {
	key := now.Format(glanceDateKey)
	for _, day := range days {
		if day.Date.Format(glanceDateKey) == key {
			return day.Sunrise, day.Sunset
		}
	}
	return time.Time{}, time.Time{}
}

// glanceDaylight shades the hourly rows before sunrise and after sunset and
// marks the sun events. Each row spans its hour, so an event sits at its
// minute within the row; its label goes in the rail at the nearest row
// boundary, between two hour labels.
func glanceDaylight(hours []HourlyForecast, sunrise, sunset time.Time) ([]glanceSpan, []glanceSunMark) {
	if len(hours) == 0 {
		return nil, nil
	}
	start, end := hours[0].Time, hours[len(hours)-1].Time.Add(time.Hour)
	px := func(at time.Time) int { return int(math.Round(at.Sub(start).Hours() * glanceHourRowPx)) }
	var night []glanceSpan
	if !sunrise.IsZero() && start.Before(sunrise) {
		night = append(night, glanceSpan{Top: 0, Height: px(minTime(sunrise, end))})
	}
	if !sunset.IsZero() && end.After(sunset) {
		from := maxTime(sunset, start)
		night = append(night, glanceSpan{Top: px(from), Height: px(end) - px(from)})
	}
	var marks []glanceSunMark
	for _, event := range []struct {
		at    time.Time
		glyph string
	}{{sunrise, "↑"}, {sunset, "↓"}} {
		if event.at.IsZero() || !event.at.After(start) || !event.at.Before(end) {
			continue
		}
		top := px(event.at)
		boundary := min(max(int(math.Round(float64(top)/glanceHourRowPx)), 1), len(hours))
		marks = append(marks, glanceSunMark{Top: top, LabelTop: boundary * glanceHourRowPx, Glyph: event.glyph, Time: event.at.Format("15:04")})
	}
	return night, marks
}

func glanceDaySummary(day DailyAggregate) string {
	rain := formatPrecip(day.PrecipSum)
	if rain == "" {
		rain = "dry"
	} else {
		rain += " mm"
	}
	summary := fmt.Sprintf("Whole day %d°/%d° · %s · gusts to %d", int(round(day.TempMax)), int(round(day.TempMin)), rain, int(round(day.GustMax)))
	if !day.Sunrise.IsZero() && !day.Sunset.IsZero() {
		summary += fmt.Sprintf(" · sun %s–%s", day.Sunrise.Format("15:04"), day.Sunset.Format("15:04"))
	}
	return summary
}

func glanceRides(rides []rideWindow) []glanceRide {
	out := make([]glanceRide, 0, len(rides))
	for _, ride := range rides {
		out = append(out, glanceRide{
			Hours:      ride.Start.Format("15") + "–" + ride.End.Format("15"),
			RainChance: ride.RainChance,
			Detail: fmt.Sprintf("%s–%s · rain chance %d%% · wind %d km/h · gusts %d km/h · feels %d° · UV %d",
				ride.Start.Format("15:04"), ride.End.Format("15:04"), ride.RainChance,
				int(round(ride.Wind)), int(round(ride.Gusts)), int(round(ride.Feels)), int(round(ride.UV))),
		})
	}
	return out
}

// glanceCSS formats an inline style from numbers only, so it is safe to
// mark as CSS.
func glanceCSS(format string, values ...float64) template.CSS {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return template.CSS(fmt.Sprintf(format, args...))
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
