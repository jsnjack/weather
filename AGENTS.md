# AGENTS.md

> See [AGENTS.universal.md](./AGENTS.universal.md), [AGENTS.go.md](./AGENTS.go.md) and [AGENTS.android.md](./AGENTS.android.md) for shared conventions.
> Refresh: `make standards`

---

## Overview

`weather` is a Go CLI for short-range rain forecasting and bike-trip planning,
optimised for the Netherlands. The root command prints a 2-hour rain chart
(Buienalarm + Buienradar) for your current location. Subcommands cover an
hour-by-hour rolling forecast (`hourly`), detailed calendar-day forecasts
(`today --forecast`, `tomorrow`, `weekend`), a multi-day daily outlook (`forecast`,
up to 16 days), shorter-term ride planning (`today`), multi-day bike-trip
planning (`multiday`, a spatial heatmap by default or ranked trip plans with
`--heatmap=false`; implemented in `multiday*.go`), and an HTTP/PWA front-end
(`serve`) that exposes the same forecasts in a browser.

**Keep every surface in sync.** A forecast view should exist on all three
surfaces: the CLI (`cmd_*.go`, termplt), the web/PWA (`serve` handler +
`web/*_head`/`_body` templates), and — when it belongs on the home screen —
the Android widget. `hourly`/`forecast` share their data layer between CLI and
web; only the rendering differs.

---

## Where the details are

This file holds the rules every session needs. The longer design notes, with
the incidents behind them, are in `docs/`. Read the file for the area before
changing it:

| Working on | Read |
|---|---|
| `android/`: the widget's layouts, colours and refresh | [docs/widget.md](docs/widget.md) |
| radar: `serve_radar_*.go`, `cmd_radar.go`, `web/radar.js`, the widget map | [docs/radar.md](docs/radar.md) |
| the web pages' look, the icons shared with the widget, `/glance` | [docs/web.md](docs/web.md) |
| Open-Meteo caching and pacing, `today`, `today --forecast` / `tomorrow` / `weekend` | [docs/forecasts.md](docs/forecasts.md) |

The widget's build and iterate workflow is in
[android/README.md](android/README.md).

This file stays under 30,000 bytes, and `make check` fails when it is over
(AGENTS.universal.md, "AGENTS.md size"). When a change alters one of these
areas, update its file in `docs/`, not this one.

---

## Architecture

```
main.go                          Thin entry point — delegates to cmd.Execute()
cmd/
  cmd_root.go                    Root cobra command — 2h rain chart, persistent flags
  cmd_hourly.go                  `hourly` command — by-hour temp/precip charts + table
  cmd_radar.go                   `radar --output` PNG export of the local observation
  cmd_forecast.go                `forecast` command — multi-day table + hi/lo chart
  logger.go                      slog wiring for --debug / --trace
  cache.go                       Bounded TTL cache with shared in-flight requests
  forecast_store.go              Atomic persistent Open-Meteo response cache
  openmeteo_http.go              Shared request pacing, concurrency and rate-limit cooldown
  forecast.go                    Shared Forecast / ForecastType / ForecastDataPoint types
  forecast_buinealarm.go         Buienalarm nowcast client (imn-rust-lb.infoplaza.io)
  forecast_buineradar.go         Buienradar nowcast client (gpsgadget.buienradar.nl)
  location.go                    ResolveLocation — priority: lat/lon > name > IP
  location_ip.go                 MaxMind IP geolocation fallback
  location_name.go               Name → coordinates (Nominatim geocoding)
  lat_lon_to_location.go         Reverse-geocode coordinates → human description
  progress.go                    CLI progress bar
  multiday.go                       Multi-leg trip search command + result rendering
  multiday_beam.go                  Beam search over bearing sequences
  multiday_fetch.go                 Open-Meteo hourly data client with retry / back-off
  multiday_geo.go                   Distance / bearing helpers
  multiday_heatmap.go               Spatial heatmap rendering (alternative multiday mode)
  multiday_score.go                 Per-day weather scoring
  serve.go                       HTTP server: HTML pages + JSON API + embedded PWA assets
  serve_forecast.go              /hourly + /forecast handlers, Open-Meteo daily fetch
  serve_location.go              Strict web location parsing and navigation state
  serve_radar_timeline.go         Cached KNMI GIF composition and seekable frame atlas
  serve_radar_local.go            Cached, georeferenced 50 km radar images for web/widget
  serve_radar_local_timeline.go   Progressive local forecast frames and cached atlas
  serve_radar_basemap.go          PDOK tile composition, shared and persistent map caching
  serve_radar_outline.go          Outline of KNMI's 30-minute rain forecast on the local map
  serve_radar_theme.go            Dark basemap/label styling that preserves weather colours
  outlook.go                     Calendar-day windows and ranked dry ride windows
  serve_outlook.go               /day, /tomorrow, /weekend and /api/v1/outlook
  serve_glance.go                Unified glance payload (rain + Open-Meteo snapshot)
  serve_glance_page.go           /glance: now, 2 hours, today and 13 days on one time axis
  svgchart.go                    Inline SVG chart for the HTML pages
  condition_icons.go             The widget's condition icons as inline SVG for the web
  today.go                       Short ride-window heatmap command
  today_fetch.go                 Batched rain/wind grid fetch for the ride heatmap
  web/                           Embedded HTML templates, manifest, service worker, icon, CSS
android/                         Native Android home-screen widget (Kotlin, separate Gradle build)
                                 Calls /api/v1/rain and mirrors svgchart.go on the device.
                                 See android/README.md for the build + iterate workflow.
```

External APIs:
- `imn-rust-lb.infoplaza.io` — Buienalarm 2-hour precipitation nowcast.
- `gpsgadget.buienradar.nl` — Buienradar 2-hour precipitation history+forecast.
- `api.open-meteo.com` — hourly temperature / precipitation / wind for multiday & today.
- `geoip.maxmind.com` — IP geolocation fallback.
- `us1.api-bdc.net` — reverse-geocoding for human-readable location names.
- `nominatim.openstreetmap.org` — place-name geocoding.

---

## Key Flows

1. **Root rain chart.** `ResolveLocation` → parallel fetch of Buienalarm +
   Buienradar via `fetchRain` → both series plotted on a single `termplt`
   line chart, capped at the Buienalarm horizon.
2. **`multiday`.** Beam search over bearing sequences from the start location.
   Each candidate fans out into ≤8 next-day bearings; per-day score from
   `multiday_score.go` combines daytime dry hours, wind, and temperature; pivot
   and round-trip penalties prune the beam to `--beam-width`. Top-N plans
   rendered as a compass-direction table.
3. **`today`.** Build an NxN lat/lon grid around the start, fetch rain and
   wind for all cells in multi-location Open-Meteo batches (`today_fetch.go`),
   compute consecutive dry-hours from ride start, render coloured grid +
   per-sector wind evolution.
4. **`serve`.** Embedded `web/*` templates streamed in two parts per page
   (`*_head` flushed immediately, `*_body` flushed after the work) so the
   browser paints the shell while forecasts fetch. JSON API mirrors the CLI
   commands under `/api/v1/`.

---

## Build & Run

```bash
make check          # fmt + vet + build + test + lint
make android-check  # widget APK + JVM tests + Android lint
make install        # build widget APK + explicit adb install -r
make build          # multi-arch binaries in bin/
./weather           # rain chart for your IP-detected location
./weather --lat 52.37 --lon 4.90
./weather multiday --days 5 --km-per-day 100
./weather today --hours 6
./weather today --forecast
./weather tomorrow
./weather weekend
./weather serve --addr :8080   # then open /glance?lat=52.37&lon=4.90
```

`make release` runs the Go and Android validation gates, uploads the CLI
archives and widget APK, then queues the shared
F-Droid publisher for Weather Widget at the exact source commit. Releases
require a clean checkout pushed to `origin/master` and authenticated GitHub CLI
access to `jsnjack/android-apps`. `make fdroid-publish` retries the trigger without
re-uploading GitHub release assets. Publication is asynchronous and preserves
Wahoo Share's published APKs; the publisher has no scheduled polling.
The Go dependencies require Go 1.26 or newer. Android builds use Gradle 9.8.1,
AGP 9.4.1 with built-in Kotlin 2.4.21, SDK 37 and build tools 37.0.0; the
target SDK is 37 (Android 17, One UI 9) and the minimum SDK is 35 (Android 15),
set by the widget picker's live-preview API. The widget must keep working on
One UI 8.5 (Android 16), so it uses no Android 17-only APIs. Gradle runs the build on
JDK 25 from `gradle/gradle-daemon-jvm.properties` and downloads it when
missing; any Java runtime on `PATH` starts the wrapper. The SDK comes from the
Android CLI (`android sdk install`); once its licence is recorded, Gradle
installs the pinned platform and build tools itself.
`make apk` stamps the widget version from `monova` through `-PappVersion`, using
the same major/minor/patch numeric mapping as the shared F-Droid publisher.

`--debug` (`-d`) prints logs to stderr; `--trace` writes maximum detail to
`/tmp/weather.log` (truncated on every start).

---

## Design Decisions

- **Android widget mirrors `svgchart.go`, not invents its own.** Colors
  (`#06b6d4` Buienalarm, `#a855f7` Buienradar), `niceStep` y-axis ticks,
  data-derived x extents, and `MinYHi=1` floor are all ported in
  `android/app/src/main/java/net/surfly/weather/widget/render/ChartRenderer.kt`
  so the home-screen widget looks like the PWA. Change both together.
- **The widget has one fixed layout per size.** `WidgetPlanner` picks it from
  the launcher's exact sizes, never from the weather, and each refresh calls
  `updateAppWidget` once, after the forecast and both map themes have loaded: a
  map that came and went with rain made the widget flicker. The rules learned by
  rejection (no bottom data strip, no provider names when dry, one dry-or-rainy
  decision on the capped chart window) are in [docs/widget.md](docs/widget.md).
- **One design language, two colour sources.** Web and widget share one
  structure and one line-icon set for sky conditions
  (`TestConditionIconsMatchWidget` fails when `condition_icons.go` and the
  widget's `ic_cond_*.xml` drift). The web has a fixed warm-paper palette and
  the widget Material You; provider line colours and caution/critical states are
  the same everywhere. The heatmap semaphore palette in `serve.go` is repeated
  in the `today_body`/`multiday_body` legends, so change both. Details in
  [docs/web.md](docs/web.md).
- **Widget colours are Material You dynamic tokens,** except the provider lines
  and the caution/critical states, which stay fixed. Every update carries both
  themes (`setIcon(…, light, dark)`, colour-state lists, `android:tint`), so the
  launcher switches theme without starting the app
  ([docs/widget.md](docs/widget.md)).
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
- **Times in the location's zone, never the server's.** Open-Meteo is
  requested with `timezone=auto` and its wall-clock strings are parsed in the
  zone the response names (`openMeteoZone` in `multiday_fetch.go`; `time/tzdata`
  is embedded via `main.go` so IANA lookups work on tzdata-less hosts), so
  every parsed time is a correct instant on any server. A UTC production
  server once stamped Amsterdam's 22:00 sunset as `22:00Z` — the widget
  (which renders instants in the phone's zone) showed midnight. When
  formatting for display, keep the zone the time already carries (the
  location's wall clock); `.In(time.Local)` reintroduces the server's zone.
- **Streaming HTML.** `serve` flushes a `_head` template before doing any
  upstream work, then the `_body` after, so first paint is independent of
  upstream latency.
- **Beam search, not exhaustive.** Multiday is bounded by `--beam-width` rather
  than enumerating every bearing sequence; otherwise a 7-day search blows
  out the Open-Meteo request budget.
- **Upstream fetches are cached, not rendered pages.** Rain providers cache for
  two minutes; Open-Meteo responses have memory and disk caches
  (`weather/forecasts-v1` under the XDG cache directory) and share four request
  slots, 250 ms spacing and a cooldown after HTTP 429. The ride heatmap
  (`today`) is one batched Open-Meteo fetch, and each location in a batch counts
  against the free limit of 600 calls a minute, so grids are capped at 23×23.
  `today --forecast`, `tomorrow` and `weekend` share their data and ride ranking
  with `/day`, `/tomorrow` and `/weekend`. Details in
  [docs/forecasts.md](docs/forecasts.md).
- **Locations stay fixed across web views.** Navigation carries exact coordinates,
  the resolved display label and the original place-name input. Location forms
  preserve coordinate precision; editing a name clears coordinates, and editing
  coordinates clears the name and label. Invalid, incomplete and non-finite
  coordinates return HTTP 400; an explicit `(0, 0)` is valid. With no name or
  coordinates, the fallback is the server’s IP location. Forward and reverse
  geocoding share bounded 24-hour caches with in-flight deduplication.
- **Radar.** The web has one radar player with Local and Netherlands coverage,
  and the widget map is always shown (`/api/v1/radar/local` with
  `always=1&size=512&outline=30`). Both follow the system theme by restyling
  only the basemap and labels, never the weather colours. Local playback uses
  one pinned KNMI forecast run, KNMI WMS requests are paced to its rate limits
  and hourly quota, and observations older than 20 minutes are rejected on
  server and phone (`RadarPolicy`). Endpoints, caches and limits are in
  [docs/radar.md](docs/radar.md).
- **Detailed forecasts replace Hourly in web navigation.** Glance comes first,
  then Today, Tomorrow and Weekend provide the hourly tables and ride windows.
  The rolling `/hourly` URL and CLI command remain available for custom
  periods and existing links. Ride planning links are labelled `Ride: today`
  and `Ride: multiday`.
- **The glance page puts every horizon on one time axis.** `/glance`, the
  widget's tap target, shows rows of 5 minutes (both nowcast providers), then 1
  hour for the rest of today, then 1 day for the next 13 days, on one rail and
  one °C scale. `glance_body` repeats the index page's radar player markup, so
  change both with `radar.js`. Details in [docs/web.md](docs/web.md).
- **Hourly tables fit the phone width.** Sky condition icons sit beside the
  temperature, with accessible names and tap-to-show labels. At widths up to
  600px, temperature/feels, rain/chance and wind/gusts share labelled two-line
  cells. Desktop keeps separate measurement columns; every forecast value
  remains available on both layouts.

---

## Gotchas

- Open-Meteo returns inconsistent array lengths if a column is missing;
  `GetOpenMeteoRange` rejects the response rather than silently zero-filling.
- `multiday` issues 100+ parallel Open-Meteo requests. The shared retry loop
  in `openmeteo_http.go` exists because a single transient 5xx otherwise turns
  into a contiguous block of "no data" cells.
