package cmd

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"strconv"
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

var indexTmpl = template.Must(template.ParseFS(webFS, "web/index.html.tmpl"))

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
		mux.HandleFunc("GET /api/v1/rain", handleRainJSON)
		mux.HandleFunc("GET /manifest.webmanifest", embedHandler("web/manifest.webmanifest", "application/manifest+json"))
		mux.HandleFunc("GET /sw.js", embedHandler("web/sw.js", "application/javascript"))
		mux.HandleFunc("GET /icon.svg", embedHandler("web/icon.svg", "image/svg+xml"))

		staticFS, err := fs.Sub(webFS, "web")
		if err != nil {
			return err
		}
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

		srv := &http.Server{
			Addr:              FlagServeAddr,
			Handler:           mux,
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

func init() {
	serveCmd.Flags().StringVar(&FlagServeAddr, "addr", "0.0.0.0:8080", "address to bind")
	rootCmd.AddCommand(serveCmd)
}

// fetchRain runs both rain providers in parallel for the given coordinates.
func fetchRain(ctx context.Context, lat, lon float64) (alarm, radar *Forecast, alarmErr, radarErr error) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		alarm, alarmErr = GetBuinealarmForecast(lat, lon)
	}()
	go func() {
		defer wg.Done()
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
}

type indexRow struct {
	Time string
	A    string
	B    string
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	lat, lon, name := locationQuery(r)
	loc, err := ResolveLocationFor(lat, lon, name)
	if err != nil {
		http.Error(w, "could not resolve location: "+err.Error(), http.StatusBadRequest)
		return
	}

	alarm, radar, _, _ := fetchRain(r.Context(), loc.Latitude, loc.Longitude)

	series := []SVGSeries{}
	var lastAlarmT time.Time
	if alarm != nil && len(alarm.Data) > 0 {
		series = append(series, SVGSeries{Name: "Buienalarm", Color: buienalarmColor, Data: alarm.Data})
		lastAlarmT = alarm.Data[len(alarm.Data)-1].Time
	}
	if radar != nil && len(radar.Data) > 0 {
		data := radar.Data
		if !lastAlarmT.IsZero() {
			cut := data
			for i, p := range data {
				if p.Time.After(lastAlarmT) {
					cut = data[:i]
					break
				}
			}
			data = cut
		}
		if len(data) > 0 {
			series = append(series, SVGSeries{Name: "Buineradar", Color: buineradarColor, Data: data})
		}
	}

	desc := ""
	if alarm != nil {
		desc = alarm.Desc
	}

	data := indexData{
		Location:        loc,
		Description:     desc,
		ChartSVG:        RenderLineChartSVG(series, SVGOpts{YUnit: " mm/h", XTimeFormat: "15:04"}),
		BuienalarmColor: buienalarmColor,
		BuineradarColor: buineradarColor,
		Rows:            mergeRows(alarm, radar),
		Now:             time.Now().Format("15:04:05"),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := indexTmpl.Execute(w, data); err != nil {
		DebugLogger.Printf("template execute: %s\n", err)
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
	alarm, radar, alarmErr, radarErr := fetchRain(r.Context(), loc.Latitude, loc.Longitude)
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
