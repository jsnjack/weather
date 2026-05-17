# AGENTS.md

> See [AGENTS.universal.md](./AGENTS.universal.md) and [AGENTS.go.md](./AGENTS.go.md) for universal conventions.
> Refresh: `make standards`

---

## Overview

`weather` is a Go CLI for short-range rain forecasting and bike-trip planning,
optimised for the Netherlands. The root command prints a 2-hour rain chart
(Buienalarm + Buienradar) for your current location. Subcommands cover
shorter-term ride planning (`today`), multi-day backpacking trip search
(`scout`), and an HTTP/PWA front-end (`serve`) that exposes the same forecasts
in a browser.

---

## Architecture

```
main.go                          Thin entry point — delegates to cmd.Execute()
cmd/
  cmd_root.go                    Root cobra command — 2h rain chart, persistent flags
  logger.go                      slog wiring for --debug / --trace
  forecast.go                    Shared Forecast / ForecastType / ForecastDataPoint types
  forecast_buinealarm.go         Buienalarm nowcast client (imn-rust-lb.infoplaza.io)
  forecast_buineradar.go         Buienradar nowcast client (gpsgadget.buienradar.nl)
  location.go                    ResolveLocation — priority: lat/lon > name > IP
  location_ip.go                 MaxMind IP geolocation fallback
  location_name.go               Name → coordinates (open-meteo geocoding)
  lat_lon_to_location.go         Reverse-geocode coordinates → human description
  progress.go                    CLI progress bar
  scout.go                       Multi-leg trip search command + result rendering
  scout_beam.go                  Beam search over bearing sequences
  scout_fetch.go                 Open-Meteo hourly data client with retry / back-off
  scout_geo.go                   Distance / bearing helpers
  scout_heatmap.go               Spatial heatmap rendering (alternative scout mode)
  scout_score.go                 Per-day weather scoring
  serve.go                       HTTP server: HTML pages + JSON API + embedded PWA assets
  svgchart.go                    Inline SVG chart for the HTML pages
  today.go                       Short ride-window heatmap command
  web/                           Embedded HTML templates, manifest, service worker, icon, CSS
```

External APIs:
- `imn-rust-lb.infoplaza.io` — Buienalarm 2-hour precipitation nowcast.
- `gpsgadget.buienradar.nl` — Buienradar 2-hour precipitation history+forecast.
- `api.open-meteo.com` — hourly temperature / precipitation / wind for scout & today.
- `geoip.maxmind.com` — IP geolocation fallback.
- `us1.api-bdc.net` — reverse-geocoding for human-readable location names.

---

## Key Flows

1. **Root rain chart.** `ResolveLocation` → parallel fetch of Buienalarm +
   Buienradar via `fetchRain` → both series plotted on a single `termplt`
   line chart, capped at the Buienalarm horizon.
2. **`scout`.** Beam search over bearing sequences from the start location.
   Each candidate fans out into ≤8 next-day bearings; per-day score from
   `scout_score.go` combines daytime dry hours, wind, and temperature; pivot
   and round-trip penalties prune the beam to `--beam-width`. Top-N plans
   rendered as a compass-direction table.
3. **`today`.** Build an NxN lat/lon grid around the start, fetch each cell's
   hourly forecast in parallel from Open-Meteo, compute consecutive dry-hours
   from ride start, render coloured grid + per-sector wind evolution.
4. **`serve`.** Embedded `web/*` templates streamed in two parts per page
   (`*_head` flushed immediately, `*_body` flushed after the work) so the
   browser paints the shell while forecasts fetch. JSON API mirrors the CLI
   commands under `/api/v1/`.

---

## Build & Run

```bash
make check          # fmt + vet + build + test + lint
make build          # multi-arch binaries in bin/
./weather           # rain chart for your IP-detected location
./weather --lat 52.37 --lon 4.90
./weather scout --days 5 --km-per-day 100
./weather today --hours 6
./weather serve --addr :8080
```

`--debug` (`-d`) prints logs to stderr; `--trace` writes maximum detail to
`/tmp/weather.log` (truncated on every start).

---

## Design Decisions

- **Both nowcast providers, same chart.** Buienalarm and Buienradar disagree
  often enough that showing both lines is more useful than picking one. The
  shorter Buienalarm horizon caps the x-axis to keep them comparable.
- **Open-Meteo elevation as a sea check.** `OpenMeteoData.IsSea()` matches on
  `Elevation == 0` because the model clamps surface water (sea, IJsselmeer)
  to NAP zero while polders return negative values. Don't broaden this to
  `<= 0` — polders will be flagged as sea.
- **Times in local zone.** Open-Meteo is requested with `timezone=auto` and
  parsed via `ParseInLocation(..., time.Local)`. All hour-of-day comparisons
  assume the user and the queried point share a timezone (true for NL use).
- **Streaming HTML.** `serve` flushes a `_head` template before doing any
  upstream work, then the `_body` after, so first paint is independent of
  upstream latency.
- **Beam search, not exhaustive.** Scout is bounded by `--beam-width` rather
  than enumerating every bearing sequence; otherwise a 7-day search blows
  out the Open-Meteo request budget.

---

## Gotchas

- Module path is `weather` (not `github.com/jsnjack/weather`). ldflags
  must stamp `weather/cmd.Version`, not the GitHub-style import path.
- Open-Meteo returns inconsistent array lengths if a column is missing;
  `GetOpenMeteoRange` rejects the response rather than silently zero-filling.
- `scout` and `today` issue 100+ parallel Open-Meteo requests. The retry loop
  in `scout_fetch.go` exists because a single transient 5xx otherwise turns
  into a contiguous block of "no data" cells.
