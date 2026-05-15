package cmd

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

//go:embed web
var webFS embed.FS

var FlagServeAddr string

const (
	buienalarmColor = "#06b6d4"
	buineradarColor = "#a855f7"
)

var tmplFuncs = template.FuncMap{
	"add": func(a, b int) int { return a + b },
}

// Each page streams in two parts: a "_head" template flushed before the work
// starts (so the browser paints the shell + progress bar immediately) and a
// "_body" template flushed after the work completes.
var (
	indexHeadTmpl = template.Must(template.New("index_head.html.tmpl").Funcs(tmplFuncs).ParseFS(webFS, "web/index_head.html.tmpl"))
	indexBodyTmpl = template.Must(template.New("index_body.html.tmpl").Funcs(tmplFuncs).ParseFS(webFS, "web/index_body.html.tmpl"))
	todayHeadTmpl = template.Must(template.New("today_head.html.tmpl").Funcs(tmplFuncs).ParseFS(webFS, "web/today_head.html.tmpl"))
	todayBodyTmpl = template.Must(template.New("today_body.html.tmpl").Funcs(tmplFuncs).ParseFS(webFS, "web/today_body.html.tmpl"))
	scoutHeadTmpl = template.Must(template.New("scout_head.html.tmpl").Funcs(tmplFuncs).ParseFS(webFS, "web/scout_head.html.tmpl"))
	scoutBodyTmpl = template.Must(template.New("scout_body.html.tmpl").Funcs(tmplFuncs).ParseFS(webFS, "web/scout_body.html.tmpl"))
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run an HTTP server that serves the forecast in a browser",
	Long: `Starts an HTTP server that exposes the same forecast as the CLI via:
  GET /                  HTML page with an inline SVG chart
  GET /api/v1/rain       JSON 2-hour rain forecast
plus a PWA shell (manifest, service worker, icon) so the page can be
installed on Android as a stand-in for a native widget.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		mux := http.NewServeMux()
		mux.HandleFunc("GET /", handleIndex)
		mux.HandleFunc("GET /today", handleToday)
		mux.HandleFunc("GET /scout", handleScout)
		mux.HandleFunc("GET /api/v1/rain", handleRainJSON)
		mux.HandleFunc("GET /api/v1/today", handleTodayJSON)
		mux.HandleFunc("GET /api/v1/scout", handleScoutJSON)
		mux.HandleFunc("GET /manifest.webmanifest", embedHandler("web/manifest.webmanifest", "application/manifest+json"))
		mux.HandleFunc("GET /sw.js", embedHandler("web/sw.js", "application/javascript"))
		mux.HandleFunc("GET /icon.svg", embedHandler("web/icon.svg", "image/svg+xml"))

		staticFS, err := fs.Sub(webFS, "web")
		if err != nil {
			return err
		}
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

		accessLog := log.New(os.Stdout, "", log.LstdFlags)
		srv := &http.Server{
			Addr:              FlagServeAddr,
			Handler:           accessLogMiddleware(accessLog, mux),
			ReadHeaderTimeout: 5 * time.Second,
		}

		fmt.Printf("Serving on http://%s\n", FlagServeAddr)
		if ip := firstLANIP(); ip != "" {
			_, port, _ := net.SplitHostPort(FlagServeAddr)
			fmt.Printf("LAN access:  http://%s:%s\n", ip, port)
		}

		return srv.ListenAndServe()
	},
}

// statusRecorder wraps a ResponseWriter so we can log the final status code
// and byte count. Implements http.Flusher explicitly so the streaming
// handlers can still flush head/progress chunks through the middleware.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func accessLogMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		path := r.URL.Path
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		logger.Printf("%s %s %s %d %dB %s",
			remoteIP(r), r.Method, path, rec.status, rec.bytes, time.Since(start).Round(time.Millisecond))
	})
}

func remoteIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if comma := strings.IndexByte(xff, ','); comma > 0 {
			return strings.TrimSpace(xff[:comma])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func init() {
	serveCmd.Flags().StringVar(&FlagServeAddr, "addr", "0.0.0.0:8080", "address to bind")
	rootCmd.AddCommand(serveCmd)
}

// fetchRain runs both rain providers in parallel for the given coordinates.
func fetchRain(ctx context.Context, lat, lon float64, prog Progress) (alarm, radar *Forecast, alarmErr, radarErr error) {
	prog.AddTotal(2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer prog.Inc(1)
		alarm, alarmErr = GetBuinealarmForecast(lat, lon)
	}()
	go func() {
		defer wg.Done()
		defer prog.Inc(1)
		radar, radarErr = GetBuineradarForecast(lat, lon)
	}()
	wg.Wait()
	return
}

type indexData struct {
	Location        Location
	Description     string
	ChartSVG        template.HTML
	BuienalarmColor string
	BuineradarColor string
	Rows            []indexRow
	Now             string
	Q               template.URL // shared lat/lon query string for nav links
}

type indexRow struct {
	Time string
	A    string
	B    string
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	// "GET /" matches any path the other patterns don't claim. Send a real
	// 404 for unknown paths instead of silently rendering the rain page.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	lat, lon, name := locationQuery(r)
	loc, err := ResolveLocationFor(lat, lon, name)
	if err != nil {
		http.Error(w, "could not resolve location: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)

	data := indexData{
		Location:        loc,
		BuienalarmColor: buienalarmColor,
		BuineradarColor: buineradarColor,
		Q:               locQuery(loc),
	}
	if err := indexHeadTmpl.Execute(w, data); err != nil {
		DebugLogger.Printf("index head execute: %s\n", err)
		return
	}
	if flusher != nil {
		flusher.Flush()
	}

	var prog Progress = NoProgress
	if flusher != nil {
		prog = NewHTTPProgress(w, flusher)
	}
	alarm, radar, _, _ := fetchRain(r.Context(), loc.Latitude, loc.Longitude, prog)
	prog.Finish()

	series := []SVGSeries{}
	var lastAlarmT time.Time
	if alarm != nil && len(alarm.Data) > 0 {
		series = append(series, SVGSeries{Name: "Buienalarm", Color: buienalarmColor, Data: alarm.Data})
		lastAlarmT = alarm.Data[len(alarm.Data)-1].Time
	}
	if radar != nil && len(radar.Data) > 0 {
		rdata := radar.Data
		if !lastAlarmT.IsZero() {
			cut := rdata
			for i, p := range rdata {
				if p.Time.After(lastAlarmT) {
					cut = rdata[:i]
					break
				}
			}
			rdata = cut
		}
		if len(rdata) > 0 {
			series = append(series, SVGSeries{Name: "Buineradar", Color: buineradarColor, Data: rdata})
		}
	}
	if alarm != nil {
		data.Description = alarm.Desc
	}
	// YUnit comes from the forecast type so HTML matches the CLI label.
	// MinYHi=1 keeps the axis at 0..1 mm/h when there is no rain, which
	// reads as "nothing happening" rather than collapsing to a sub-0.1
	// span with repeating identical tick labels.
	yUnit := PrecipitationForecast.Unit()
	data.ChartSVG = RenderLineChartSVG(series, SVGOpts{YUnit: yUnit, XTimeFormat: "15:04", MinYHi: 1})
	data.Rows = mergeRows(alarm, radar)
	data.Now = time.Now().Format("15:04:05")

	if err := indexBodyTmpl.Execute(w, data); err != nil {
		DebugLogger.Printf("index body execute: %s\n", err)
	}
}

type rainAPIResponse struct {
	Location   Location  `json:"location"`
	Buienalarm *Forecast `json:"buienalarm"`
	Buineradar *Forecast `json:"buineradar"`
}

func handleRainJSON(w http.ResponseWriter, r *http.Request) {
	lat, lon, name := locationQuery(r)
	loc, err := ResolveLocationFor(lat, lon, name)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	alarm, radar, alarmErr, radarErr := fetchRain(r.Context(), loc.Latitude, loc.Longitude, NoProgress)
	if alarm == nil && radar == nil {
		writeJSONError(w, http.StatusBadGateway, errors.Join(alarmErr, radarErr))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(rainAPIResponse{Location: loc, Buienalarm: alarm, Buineradar: radar})
}

func writeJSONError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func locationQuery(r *http.Request) (lat, lon float64, name string) {
	q := r.URL.Query()
	if v := q.Get("lat"); v != "" {
		lat, _ = strconv.ParseFloat(v, 64)
	}
	if v := q.Get("lon"); v != "" {
		lon, _ = strconv.ParseFloat(v, 64)
	}
	name = q.Get("name")
	return
}

func embedHandler(path, contentType string) http.HandlerFunc {
	data, err := webFS.ReadFile(path)
	return func(w http.ResponseWriter, r *http.Request) {
		if err != nil {
			http.Error(w, "asset missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(data)
	}
}

// mergeRows builds a table joining buienalarm and buineradar values by time.
// Anchors on buienalarm timestamps when present, otherwise buineradar.
func mergeRows(a, b *Forecast) []indexRow {
	primary := []ForecastDataPoint{}
	if a != nil && len(a.Data) > 0 {
		primary = a.Data
	} else if b != nil && len(b.Data) > 0 {
		primary = b.Data
	}
	rows := make([]indexRow, 0, len(primary))
	for _, p := range primary {
		row := indexRow{Time: p.Time.Format("15:04"), A: "—", B: "—"}
		if a != nil {
			if v, ok := nearest(a.Data, p.Time); ok {
				row.A = fmt.Sprintf("%.2f", v)
			}
		}
		if b != nil {
			if v, ok := nearest(b.Data, p.Time); ok {
				row.B = fmt.Sprintf("%.2f", v)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func nearest(points []ForecastDataPoint, t time.Time) (float64, bool) {
	const tolerance = 6 * time.Minute
	best := -1
	bestDelta := tolerance + time.Second
	for i, p := range points {
		d := p.Time.Sub(t)
		if d < 0 {
			d = -d
		}
		if d < bestDelta {
			best = i
			bestDelta = d
		}
	}
	if best < 0 {
		return 0, false
	}
	return points[best].Value, true
}

// locQuery returns "lat=...&lon=..." for the resolved location so nav links
// preserve the user's place when hopping between pages. Returned as
// template.URL so html/template doesn't percent-encode the & and = chars.
func locQuery(loc Location) template.URL {
	return template.URL(fmt.Sprintf("lat=%.4f&lon=%.4f", loc.Latitude, loc.Longitude))
}

// ---------- /today ----------

func parseTodayParams(r *http.Request) (hours int, start time.Time, radius float64, grid int, startInput string) {
	q := r.URL.Query()
	hours = 6
	if v, err := strconv.Atoi(q.Get("hours")); err == nil && v >= 1 && v <= 24 {
		hours = v
	}
	radius = 50
	if v, err := strconv.ParseFloat(q.Get("radius"), 64); err == nil && v > 0 {
		radius = v
	}
	grid = 21
	if v, err := strconv.Atoi(q.Get("grid")); err == nil && v >= 5 {
		grid = v
	}
	startInput = q.Get("start")
	now := time.Now()
	if startInput == "" {
		t := now.Add(30 * time.Minute).Truncate(time.Hour).Add(time.Hour)
		start = t
		startInput = t.Format("15:04")
		return
	}
	if parsed, err := time.Parse("15:04", startInput); err == nil {
		start = time.Date(now.Year(), now.Month(), now.Day(),
			parsed.Hour(), parsed.Minute(), 0, 0, now.Location())
		return
	}
	t := now.Add(30 * time.Minute).Truncate(time.Hour).Add(time.Hour)
	start = t
	startInput = t.Format("15:04")
	return
}

func handleTodayJSON(w http.ResponseWriter, r *http.Request) {
	lat, lon, name := locationQuery(r)
	loc, err := ResolveLocationFor(lat, lon, name)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	hours, start, radius, grid, _ := parseTodayParams(r)
	result := runTodayGrid(loc.Latitude, loc.Longitude, start, hours, grid, radius, NoProgress)
	rec := RecommendToday(result)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"location":       loc,
		"result":         result,
		"recommendation": rec,
	})
}

type todaySectorRow struct {
	Name      string
	Cells     []string
	OverWater bool
}

type todayPageData struct {
	// inputs (set before head render)
	Location    Location
	Q           template.URL
	WindowHours int
	Grid        int
	RadiusKm    float64
	StartLabel  string
	EndLabel    string
	StartInput  string
	// results (set before body render)
	Recommendation TodayRecommendation
	HeatmapSVG     template.HTML
	HourLabels     []string
	SectorRows     []todaySectorRow
	BestDesc       string
	BestWind       string
	WorstDesc      string
	Now            string
}

func handleToday(w http.ResponseWriter, r *http.Request) {
	lat, lon, name := locationQuery(r)
	loc, err := ResolveLocationFor(lat, lon, name)
	if err != nil {
		http.Error(w, "could not resolve location: "+err.Error(), http.StatusBadRequest)
		return
	}
	hours, start, radius, grid, startInput := parseTodayParams(r)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)

	page := todayPageData{
		Location:    loc,
		Q:           locQuery(loc),
		WindowHours: hours,
		Grid:        grid,
		RadiusKm:    radius,
		StartLabel:  start.Format("15:04"),
		EndLabel:    start.Add(time.Duration(hours) * time.Hour).Format("15:04"),
		StartInput:  startInput,
	}
	if err := todayHeadTmpl.Execute(w, page); err != nil {
		DebugLogger.Printf("today head execute: %s\n", err)
		return
	}
	if flusher != nil {
		flusher.Flush()
	}

	var prog Progress = NoProgress
	if flusher != nil {
		prog = NewHTTPProgress(w, flusher)
	}
	result := runTodayGrid(loc.Latitude, loc.Longitude, start, hours, grid, radius, prog)
	prog.Finish()

	rec := RecommendToday(result)
	gridCells := make([][]GridCell, len(result.Cells))
	mid := result.Grid / 2
	for rIdx, row := range result.Cells {
		gridCells[rIdx] = make([]GridCell, len(row))
		for cIdx, cell := range row {
			gridCells[rIdx][cIdx] = todayCellToGrid(cell, hours, rIdx == mid && cIdx == mid)
		}
	}
	page.HeatmapSVG = RenderHeatGridSVG(gridCells, GridOpts{
		CellSize: 22,
		StepKm:   result.StepKm,
		Title:    fmt.Sprintf("Rain timing  ·  %dh window", hours),
	})
	hourLabels := make([]string, 0, hours)
	for i := 0; i < hours; i++ {
		hourLabels = append(hourLabels, start.Add(time.Duration(i)*time.Hour).Format("15"))
	}
	page.HourLabels = hourLabels
	for _, s := range result.Sectors {
		row := todaySectorRow{Name: s.Name, OverWater: s.OverWater}
		if s.NoData {
			row.Cells = []string{"(no data)"}
			page.SectorRows = append(page.SectorRows, row)
			continue
		}
		for _, w := range s.Wind {
			if w.Speed <= windCalmKmh {
				row.Cells = append(row.Cells, "·")
			} else {
				row.Cells = append(row.Cells, CompassArrow(w.BlowsTo))
			}
		}
		page.SectorRows = append(page.SectorRows, row)
	}
	page.Recommendation = rec
	if len(rec.Rideable) > 0 {
		page.BestDesc = describeDry(rec.Best.DryHours, hours)
		page.BestWind = describeWind(rec.Best.Tailwind, rec.Best.Cell.WindSpeed)
		page.WorstDesc = describeDry(rec.Worst.DryHours, hours)
	}
	page.Now = time.Now().Format("15:04:05")

	if err := todayBodyTmpl.Execute(w, page); err != nil {
		DebugLogger.Printf("today body execute: %s\n", err)
	}
}

func todayCellToGrid(c todayCell, windowHours int, isStart bool) GridCell {
	if isStart {
		sc := "#fff"
		if c.Sea {
			sc = "#06b6d4" // start over water keeps the sea-cyan tint
		}
		return GridCell{Color: todayBandHex(rainTimingBand(c.DryHours, windowHours), c.NoData), Symbol: "●", SymbolColor: sc, Border: "#fff"}
	}
	if c.NoData {
		return GridCell{Color: "#3f3f46", Symbol: ""}
	}
	band := rainTimingBand(c.DryHours, windowHours)
	bg := todayBandHex(band, false)
	if band == 3 {
		// Tint the ✗ cyan over water so the "this is water" cue isn't lost
		// behind the rain colour.
		sym := "✗"
		sc := "#fff"
		if c.Sea {
			sc = "#06b6d4"
		}
		return GridCell{Color: bg, Symbol: sym, SymbolColor: sc}
	}
	sym := ""
	if WindBand(c.WindSpeed) > 0 {
		sym = CompassArrow(c.WindBlowsTo)
	} else {
		sym = "·"
	}
	sc := "#111"
	if c.Sea {
		sc = "#06b6d4"
	}
	return GridCell{Color: bg, Symbol: sym, SymbolColor: sc}
}

func todayBandHex(band int, noData bool) string {
	if noData {
		return "#3f3f46"
	}
	switch band {
	case 0:
		return "#86efac" // bright green — dry full window
	case 1:
		return "#4ade80" // green — rain late
	case 2:
		return "#facc15" // yellow — rain middle
	default:
		return "#ef4444" // red — raining now
	}
}

// ---------- /scout ----------

type scoutQuery struct {
	Days             int
	KmPerDay         float64
	MinTemp          float64
	StartDate        time.Time
	StartDateInput   string
	BeamWidth        int
	PivotPenalty     float64
	RoundTrip        bool
	RoundTripPenalty float64
	TopN             int
	Heatmap          bool
	HeatmapGrid      int
}

func parseScoutParams(r *http.Request) scoutQuery {
	q := r.URL.Query()
	out := scoutQuery{
		Days: 5, KmPerDay: 100, MinTemp: 15,
		BeamWidth: 16, PivotPenalty: 3,
		RoundTripPenalty: 20, TopN: 3,
		HeatmapGrid: 21,
	}
	if v, err := strconv.Atoi(q.Get("days")); err == nil && v > 0 && v <= 14 {
		out.Days = v
	}
	if v, err := strconv.ParseFloat(q.Get("km-per-day"), 64); err == nil && v > 0 {
		out.KmPerDay = v
	}
	if v, err := strconv.ParseFloat(q.Get("min-temp"), 64); err == nil {
		out.MinTemp = v
	}
	if v, err := strconv.Atoi(q.Get("beam-width")); err == nil && v > 0 {
		out.BeamWidth = v
	}
	if v, err := strconv.ParseFloat(q.Get("pivot-penalty"), 64); err == nil {
		out.PivotPenalty = v
	}
	if q.Get("round-trip") != "" {
		out.RoundTrip = true
	}
	if v, err := strconv.ParseFloat(q.Get("round-trip-penalty"), 64); err == nil {
		out.RoundTripPenalty = v
	}
	if v, err := strconv.Atoi(q.Get("top")); err == nil && v > 0 {
		out.TopN = v
	}
	if q.Get("heatmap") != "" {
		out.Heatmap = true
	}
	if v, err := strconv.Atoi(q.Get("heatmap-grid")); err == nil && v >= 5 {
		out.HeatmapGrid = v
	}
	out.StartDateInput = q.Get("start-date")
	if out.StartDateInput != "" {
		if t, err := time.Parse("2006-01-02", out.StartDateInput); err == nil {
			out.StartDate = t
		}
	}
	if out.StartDate.IsZero() {
		out.StartDate = time.Now()
		out.StartDateInput = out.StartDate.Format("2006-01-02")
	}
	return out
}

func (sq scoutQuery) Config() beamConfig {
	return beamConfig{
		KmPerDay: sq.KmPerDay, MinTemp: sq.MinTemp,
		BeamWidth: sq.BeamWidth, PivotPenalty: sq.PivotPenalty,
		RoundTrip: sq.RoundTrip, RoundTripPenalty: sq.RoundTripPenalty,
	}
}

func (sq scoutQuery) Encode() string {
	return fmt.Sprintf("days=%d&km-per-day=%.0f&min-temp=%.0f&start-date=%s&top=%d&round-trip=%t&heatmap=%t",
		sq.Days, sq.KmPerDay, sq.MinTemp, sq.StartDateInput, sq.TopN, sq.RoundTrip, sq.Heatmap)
}

type scoutTripJSON struct {
	Score       float64    `json:"score"`
	Bearings    []float64  `json:"bearings"`
	Positions   []latLon   `json:"positions"`
	DailyScores []DayScore `json:"dailyScores"`
	Labels      []string   `json:"labels"`
}

func handleScoutJSON(w http.ResponseWriter, r *http.Request) {
	lat, lon, name := locationQuery(r)
	loc, err := ResolveLocationFor(lat, lon, name)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err)
		return
	}
	sq := parseScoutParams(r)
	cfg := sq.Config()

	resp := map[string]any{
		"location": loc,
		"config": map[string]any{
			"days":      sq.Days,
			"kmPerDay":  sq.KmPerDay,
			"minTemp":   sq.MinTemp,
			"startDate": sq.StartDate.Format("2006-01-02"),
			"roundTrip": sq.RoundTrip,
		},
	}

	if sq.Heatmap {
		hm := RunHeatmap(loc.Latitude, loc.Longitude, sq.StartDate, sq.Days, cfg, sq.HeatmapGrid, NoProgress)
		resp["heatmap"] = hm
	} else {
		trips := RunBeamSearch(loc.Latitude, loc.Longitude, sq.StartDate, sq.Days, cfg, NoProgress)
		if len(trips) > sq.TopN {
			trips = trips[:sq.TopN]
		}
		labels := annotateTripLabels(trips, NoProgress)
		out := make([]scoutTripJSON, len(trips))
		for i, t := range trips {
			out[i] = scoutTripJSON{
				Score: t.Score, Bearings: t.Bearings, Positions: t.Positions,
				DailyScores: t.DailyScores, Labels: labels[i],
			}
		}
		resp["trips"] = out
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

type scoutTripRow struct {
	Dir       string
	Label     string
	Temp      string
	Cold      bool
	Wind      string
	WindColor string
}

type scoutTripView struct {
	Score     float64
	Path      string
	EndLabel  string
	EndDistKm float64
	Days      []scoutTripRow
}

type scoutPageData struct {
	Location           Location
	Q                  template.URL
	Cfg                scoutPageCfg
	IsHeatmap          bool
	StartLabel         string
	EndLabel           string
	StartInput         string
	Trips              []scoutTripView
	HeatmapDaysSVG     []template.HTML
	RecommendationText string
	JSONQuery          template.URL
	Now                string
}

type scoutPageCfg struct {
	Days         int
	KmPerDay     float64
	MinTemp      float64
	MinTempPlus5 float64 // = MinTemp + 5, pre-computed for the heatmap legend
	MinTempMinus5 float64 // = MinTemp - 5
	TopN         int
	RoundTrip    bool
}

func handleScout(w http.ResponseWriter, r *http.Request) {
	lat, lon, name := locationQuery(r)
	loc, err := ResolveLocationFor(lat, lon, name)
	if err != nil {
		http.Error(w, "could not resolve location: "+err.Error(), http.StatusBadRequest)
		return
	}
	sq := parseScoutParams(r)
	cfg := sq.Config()
	endDate := sq.StartDate.AddDate(0, 0, sq.Days-1)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	flusher, _ := w.(http.Flusher)

	page := scoutPageData{
		Location: loc,
		Q:        locQuery(loc),
		Cfg: scoutPageCfg{
			Days: sq.Days, KmPerDay: sq.KmPerDay, MinTemp: sq.MinTemp,
			MinTempPlus5: sq.MinTemp + 5, MinTempMinus5: sq.MinTemp - 5,
			TopN: sq.TopN, RoundTrip: sq.RoundTrip,
		},
		IsHeatmap:  sq.Heatmap,
		StartLabel: sq.StartDate.Format("2006-01-02"),
		EndLabel:   endDate.Format("2006-01-02"),
		StartInput: sq.StartDateInput,
		JSONQuery:  template.URL(string(locQuery(loc)) + "&" + sq.Encode()),
	}
	if err := scoutHeadTmpl.Execute(w, page); err != nil {
		DebugLogger.Printf("scout head execute: %s\n", err)
		return
	}
	if flusher != nil {
		flusher.Flush()
	}

	var prog Progress = NoProgress
	if flusher != nil {
		prog = NewHTTPProgress(w, flusher)
	}

	if sq.Heatmap {
		hm := RunHeatmap(loc.Latitude, loc.Longitude, sq.StartDate, sq.Days, cfg, sq.HeatmapGrid, prog)
		prog.Finish()
		page.HeatmapDaysSVG = scoutHeatmapToSVG(hm)
	} else {
		trips := RunBeamSearch(loc.Latitude, loc.Longitude, sq.StartDate, sq.Days, cfg, prog)
		if len(trips) > sq.TopN {
			trips = trips[:sq.TopN]
		}
		labels := annotateTripLabels(trips, prog)
		prog.Finish()
		for i, t := range trips {
			page.Trips = append(page.Trips, tripToView(t, labels[i], loc.Latitude, loc.Longitude, sq.RoundTrip))
		}
		if len(trips) > 0 {
			page.RecommendationText = summarizeWinner(trips[0], labels[0], sq.RoundTrip)
		}
	}
	page.Now = time.Now().Format("15:04:05")

	if err := scoutBodyTmpl.Execute(w, page); err != nil {
		DebugLogger.Printf("scout body execute: %s\n", err)
	}
}

func tripToView(t beamNode, labels []string, startLat, startLon float64, roundTrip bool) scoutTripView {
	v := scoutTripView{Score: t.Score}
	parts := make([]string, 0, len(t.Bearings))
	for _, b := range t.Bearings {
		parts = append(parts, CompassName(b))
	}
	v.Path = strings.Join(parts, " → ")
	end := t.Positions[len(t.Positions)-1]
	v.EndDistKm = HaversineKm(end.Lat, end.Lon, startLat, startLon)
	if len(labels) > 0 {
		v.EndLabel = labels[len(labels)-1]
	}
	for i, b := range t.Bearings {
		ds := t.DailyScores[i]
		row := scoutTripRow{
			Dir:  fmt.Sprintf("%s %s", CompassName(b), CompassArrow(b)),
			Temp: fmt.Sprintf("%.0f°", ds.MaxTemp),
			Cold: ds.BelowMinTemp,
		}
		if i < len(labels) {
			row.Label = labels[i]
		}
		switch {
		case ds.TailwindAvg > tailHeadSwitchKmh:
			row.Wind = fmt.Sprintf("T%.0f", ds.TailwindAvg)
			row.WindColor = "#4ade80"
		case ds.TailwindAvg < -tailHeadSwitchKmh:
			row.Wind = fmt.Sprintf("H%.0f", -ds.TailwindAvg)
			row.WindColor = "#ef4444"
		default:
			row.Wind = "·"
			row.WindColor = "var(--muted)"
		}
		v.Days = append(v.Days, row)
	}
	return v
}

func summarizeWinner(winner beamNode, labels []string, roundTrip bool) string {
	endLabel := "the endpoint"
	if len(labels) > 0 {
		endLabel = "~" + labels[len(labels)-1]
	}
	minT, maxT := math.MaxFloat64, -math.MaxFloat64
	twSum, twCount := 0.0, 0
	pivots := countPivots(winner.Bearings)
	for _, ds := range winner.DailyScores {
		if ds.MaxTemp > maxT {
			maxT = ds.MaxTemp
		}
		if ds.MinTemp < minT {
			minT = ds.MinTemp
		}
		twSum += ds.TailwindAvg
		twCount++
	}
	twAvg := 0.0
	if twCount > 0 {
		twAvg = twSum / float64(twCount)
	}
	shape := "a straight bearing"
	if pivots == 1 {
		shape = "one pivot"
	} else if pivots > 1 {
		shape = fmt.Sprintf("%d pivots", pivots)
	}
	var wind string
	switch {
	case twAvg > tailHeadSwitchKmh:
		wind = fmt.Sprintf("avg tailwind %+.0f km/h", twAvg)
	case twAvg < -tailHeadSwitchKmh:
		wind = fmt.Sprintf("avg headwind %.0f km/h", -twAvg)
	default:
		wind = "mostly crosswind"
	}
	return fmt.Sprintf("Trip 1 — %s with %s, %.0f–%.0f°C, %s.", endLabel, shape, minT, maxT, wind)
}

func scoutHeatmapToSVG(h heatmapResult) []template.HTML {
	out := make([]template.HTML, 0, len(h.Days))
	for d, day := range h.Days {
		cells := make([][]GridCell, h.Grid)
		mid := h.Grid / 2
		for r := 0; r < h.Grid; r++ {
			cells[r] = make([]GridCell, h.Grid)
			for c := 0; c < h.Grid; c++ {
				cells[r][c] = heatmapCellToGrid(h.Cells[d][r][c], r == mid && c == mid)
			}
		}
		out = append(out, RenderHeatGridSVG(cells, GridOpts{
			CellSize: 20,
			StepKm:   h.StepKm,
			Title:    fmt.Sprintf("Day %d  ·  %s", d+1, day.Format("2006-01-02")),
		}))
	}
	return out
}

func heatmapCellToGrid(c cellStatus, isStart bool) GridCell {
	bg := heatmapBandHex(c)
	sym := ""
	sc := "#111"
	switch {
	case c.NoData:
		bg = "#3f3f46"
	case c.Sea:
		bg = "#0e7490"
		sym = "~"
		sc = "#ecfeff"
	case c.Rain:
		bg = "#3b82f6"
		sym = "·"
		sc = "#0a0a0a"
	case c.Gust:
		bg = "#ef4444"
		sym = "✗"
		sc = "#fff"
	default:
		switch c.WindBand {
		case 1:
			sym = "·"
		case 2:
			sym = "~"
		case 3:
			sym = "≈"
		}
	}
	if isStart {
		return GridCell{Color: bg, Symbol: "●", SymbolColor: "#fff", Border: "#fff"}
	}
	return GridCell{Color: bg, Symbol: sym, SymbolColor: sc}
}

func heatmapBandHex(c cellStatus) string {
	switch c.TempBand {
	case 3:
		return "#86efac"
	case 2:
		return "#4ade80"
	case 1:
		return "#facc15"
	default:
		return "#52525b"
	}
}

func firstLANIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() {
			continue
		}
		ip := ipn.IP.To4()
		if ip == nil {
			continue
		}
		return ip.String()
	}
	return ""
}
