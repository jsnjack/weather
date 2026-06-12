package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// KNMI publishes a ready-composited national radar loop — precipitation +
// lightning + temperature bubbles + legend, all baked into one animated GIF.
// It is NL-only and fixed-frame (can't centre on the user), which matches the
// rain view's existing NL-optimised, national-overview scope. We just relay it
// rather than reproducing the map ourselves; see AGENTS.md for why the raw
// KNMI Data Platform (HDF5/NetCDF, key-gated) wasn't worth it.
const knmiRadarMapURL = "https://cdn.knmi.nl/knmi/map/general/weather-map.gif"

// radarMapCache holds the last-fetched GIF bytes process-wide for a short TTL,
// so a burst of page loads doesn't hammer KNMI's CDN. The map refreshes every
// few minutes upstream; 3 min keeps it current without re-fetching per client.
// The cached []byte is shared — treat it as read-only.
var radarMapCache = newTTLCache[[]byte](3*time.Minute, 1)

// getKNMIRadarMap returns the KNMI national radar GIF, cached process-wide.
func getKNMIRadarMap() ([]byte, error) {
	return memo(radarMapCache, "knmi", func() ([]byte, error) {
		slog.Debug("knmi radar: fetching map", "url", knmiRadarMapURL)
		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequest("GET", knmiRadarMapURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer closeBody(resp.Body, "knmi radar response body")
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("unexpected status code %d", resp.StatusCode)
		}
		return io.ReadAll(resp.Body)
	})
}

// handleRadarMap relays the KNMI radar GIF. It is location-independent (a fixed
// national frame), so there's no lat/lon query to thread through. A short
// browser cache mirrors the process cache; on upstream failure the rain page's
// line chart still carries the view, so a 502 here is non-fatal to the page.
func handleRadarMap(w http.ResponseWriter, r *http.Request) {
	gif, err := getKNMIRadarMap()
	if err != nil {
		slog.Debug("knmi radar: fetch failed", "err", err)
		http.Error(w, "radar map unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Cache-Control", "public, max-age=180")
	if _, werr := w.Write(gif); werr != nil {
		slog.Log(r.Context(), LevelTrace, "write radar map", "err", werr)
	}
}
