# AGENTS.md

> See [AGENTS.universal.md](./AGENTS.universal.md) and [AGENTS.go.md](./AGENTS.go.md) for universal conventions.
> Refresh: `make standards`

---

## Overview

`weather` is a Go CLI for short-range rain forecasting and bike-trip planning,
optimised for the Netherlands. The root command prints a 2-hour rain chart
(Buienalarm + Buienradar) for your current location. Subcommands cover an
hour-by-hour day forecast (`hourly`), a multi-day daily outlook (`forecast`,
up to 16 days), shorter-term ride planning (`today`), multi-day backpacking
trip search (`scout`), and an HTTP/PWA front-end (`serve`) that exposes the
same forecasts in a browser.

**Keep every surface in sync.** A forecast view should exist on all three
surfaces: the CLI (`cmd_*.go`, termplt), the web/PWA (`serve` handler +
`web/*_head`/`_body` templates), and — when it belongs on the home screen —
the Android widget. `hourly`/`forecast` share their data layer between CLI and
web; only the rendering differs.

---

## Architecture

```
main.go                          Thin entry point — delegates to cmd.Execute()
cmd/
  cmd_root.go                    Root cobra command — 2h rain chart, persistent flags
  cmd_hourly.go                  `hourly` command — by-hour temp/precip charts + table
  cmd_forecast.go                `forecast` command — multi-day table + hi/lo chart
  logger.go                      slog wiring for --debug / --trace
  cache.go                       Process-wide TTL cache wrapping the upstream fetchers
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
  serve_forecast.go              /hourly + /forecast handlers, Open-Meteo daily fetch
  serve_glance.go                Unified glance payload (rain + Open-Meteo snapshot)
  svgchart.go                    Inline SVG chart for the HTML pages
  today.go                       Short ride-window heatmap command
  web/                           Embedded HTML templates, manifest, service worker, icon, CSS
android/                         Native Android home-screen widget (Kotlin, separate Gradle build)
                                 Calls /api/v1/rain and mirrors svgchart.go on the device.
                                 See android/README.md for the build + iterate workflow.
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

- **Android widget mirrors `svgchart.go`, not invents its own.** Colors
  (`#06b6d4` Buienalarm, `#a855f7` Buienradar), `niceStep` y-axis ticks,
  data-derived x extents, and `MinYHi=1` floor are all ported in
  `android/app/src/main/java/net/surfly/weather/widget/render/ChartRenderer.kt`
  so the home-screen widget looks like the PWA. Change both together.
- **Dry widget state is native Material views, not a bitmap.** Rainy renders
  the dual-provider chart into the `R.id.chart` bitmap (`ChartRenderer`). Dry
  hides that and shows `R.id.dry_body` — a **native RemoteViews** Material 3
  layout in `widget_rain.xml` (`RainWidgetWorker.applyBody` toggles visibility
  and `populateDryBody` fills it). Native text is crisp; the bitmap is `fitXY`
  and visibly **squishes** everything (the reason every bitmap-drawn dry hero
  looked soft/cheap and got rejected). Layout: left = warm-tinted sun/condition
  icon + a big centered light-weight NOW temp + small warm +2H; right = rounded
  tonal `surfaceContainer` (`@drawable/dry_panel_background`,
  `@color/widget_container`) with a **NOW / +2H column header** over a
  feels/wind/UV table — the header is what makes "which number is which"
  unambiguous (two unlabelled columns read as gibberish). The Buienalarm
  headline (`R.id.dry_headline`) is a small full-width caption line at the top
  of `dry_body` (up to 2 lines) so a long nowcast message isn't cropped; the
  shared `R.id.peak` line stays blank in the dry state. Today's sunset
  (`glanceAPIResponse.Sunset` → `R.id.dry_sunset_row`) fills the lower-left; the
  row hides itself when the server doesn't send the field (older deploys), so
  redeploy `weather serve` for it to appear. Wind/UV values get
  caution/critical colouring (`windColor`/`uvColor`, thresholds mirror
  `serve_glance.go`). Hard rules learned by rejection: **no bottom data strip**
  (flat dry data reads as a fake progress bar) and **no provider names** in the
  dry state. `ChartRenderer.drawHero` remains only as a defensive fallback if
  `render()` is ever reached with a dry window. **Dry vs rainy is decided once,
  on the capped chart window** (`chartWindow` in `ChartRenderer.kt`):
  `isDryWindow` and `render` must judge identical data. Radar rain beyond the
  Buienalarm horizon once split the two — `applyBody` dressed the widget in
  rainy chrome (headline duplicated into `R.id.peak`, native panel hidden)
  around the `drawHero` fallback bitmap.
- **Android widget has a periodic refresh floor.** `USER_PRESENT` unlock
  broadcasts are context-registered from `RainWidgetApp` and enqueue expedited
  one-shots while the process is alive; keep the 15-minute WorkManager periodic
  refresh armed from widget enable/update/save so automatic updates still work
  after Android kills the process.
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
- **Upstream fetches are cached, not the rendered pages.** `cache.go` wraps the
  four upstream fetchers (Open-Meteo hourly/daily, Buienalarm, Buienradar) with
  a process-wide TTL cache so flipping between web views doesn't re-issue the
  same requests — the scout/today fan-outs (100+ calls) are the expensive case.
  Cached values are shared pointers/slices: **treat them as read-only.** The CLI
  gets a fresh cache per process, so it only dedupes within one run.

---

## Gotchas

- Open-Meteo returns inconsistent array lengths if a column is missing;
  `GetOpenMeteoRange` rejects the response rather than silently zero-filling.
- `scout` and `today` issue 100+ parallel Open-Meteo requests. The retry loop
  in `scout_fetch.go` exists because a single transient 5xx otherwise turns
  into a contiguous block of "no data" cells.
