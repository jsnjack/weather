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
  serve_radar_theme.go            Dark basemap/label styling that preserves weather colours
  outlook.go                     Calendar-day windows and ranked dry ride windows
  serve_outlook.go               /day, /tomorrow, /weekend and /api/v1/outlook
  serve_glance.go                Unified glance payload (rain + Open-Meteo snapshot)
  svgchart.go                    Inline SVG chart for the HTML pages
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
./weather serve --addr :8080
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
minimum SDK remains 31 and target SDK remains 34. Make reports missing SDK
components before Gradle can download them automatically.
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
- **Dry widget state is native Material views, not a bitmap.** Rainy renders
  the dual-provider chart into the `R.id.chart` bitmap (`ChartRenderer`) on a
  neutral island tile — but its temps and feels/wind/UV micros live in the
  **native** `R.id.rain_stats` row under the island (`populateRainStats`),
  corner-anchored beneath the bitmap's time labels; only axis text stays in
  the bitmap, and the bitmap is rendered at the island's **estimated
  on-screen size** (`chartSizePx` — chrome constants mirror
  `widget_rain.xml`) so `fitXY` has nothing to stretch — a bitmap drawn at
  the raw `MAX_WIDTH×MAX_HEIGHT` option size displays at ~half height and
  flattens every label. Dry hides that and shows `R.id.dry_body` — **native
  RemoteViews** Material 3 island cells in `widget_rain.xml`
  (`RainWidgetWorker.applyBody` toggles visibility, `populateDryBody` fills
  them). Native text is crisp; the bitmap is `fitXY` and visibly **squishes**
  everything (the reason every bitmap-drawn dry hero looked soft/cheap and got
  rejected). Layout: left = accent island (`@drawable/island_accent`) with the
  condition icon, a big light-weight NOW temp, small warm +2H and the sunset
  row; right = neutral island (`@drawable/island_neutral`) with a
  **NOW / +2H column header** over a feels/wind/UV table — the header is what
  makes "which number is which" unambiguous (two unlabelled columns read as
  gibberish). The nowcast message lives in `R.id.headline`, a full-width
  **quiet caption** below the islands (11sp, muted, centred, no box — it is
  commentary, not content) **shared by both states** so it is never cropped
  into a corner; the worker collapses it when there is no text. Islands use
  16dp radii
  (outer 28dp minus 12dp padding) with 8dp gutters. Today's sunset
  (`glanceAPIResponse.Sunset` → `R.id.dry_sunset_row`) hides itself when the
  server doesn't send the field (older deploys), so redeploy `weather serve`
  for it to appear. Wind/UV values get caution/critical colouring
  (`windColor`/`uvColor`, thresholds mirror `serve_glance.go`). Hard rules
  learned by rejection: **no bottom data strip**
  (flat dry data reads as a fake progress bar) and **no provider names** in the
  dry state. `ChartRenderer.drawHero` remains only as a defensive fallback if
  `render()` is ever reached with a dry window. **Dry vs rainy is decided once,
  on the capped chart window** (`chartWindow` in `ChartRenderer.kt`):
  `isDryWindow` and `render` must judge identical data. Radar rain beyond the
  Buienalarm horizon once split the two — `applyBody` dressed the widget in
  rainy chrome (headline duplicated into `R.id.peak`, native panel hidden)
  around the `drawHero` fallback bitmap.
- **One design language, two colour sources.** Web and widget share the same
  structure — islands on a card (14–16px radii), an accent island for the NOW
  hero, labelled now/+2h stats, a quiet centred caption for the nowcast
  message, pill-shaped meta controls. The **web is warm paper** (fixed
  palette in `web/styles.css` tokens; the values the widget used before going
  dynamic) while the **widget is Material You** (wallpaper tokens). Provider
  line colours and caution/critical states are identical everywhere. The
  today/multiday heatmap semaphore palette lives in `serve.go` **and** is
  duplicated in the legend swatches of `today_body`/`multiday_body` — change
  both together. Legends are content, never strip them. The semaphore is
  **deliberately saturated** (#86efac/#4ade80/#facc15/#ef4444 on square
  cells): a "tasteful" warm retune was rejected — softened hues blur the
  green/yellow/red identities the legend names, and rounded gap tiles read
  worse than the solid grid. No refresh buttons in headers — the browser
  has one.
- **Widget colours are Material You dynamic tokens.** `values/colors.xml` maps
  every neutral and the warm accent to `@android:color/system_*` roles
  (wallpaper-derived, API 31+); `values-night/colors.xml` uses the same token
  families a tonal step apart, so dark mode comes from the palette, not a
  hand-mixed second theme. Only the provider lines (`#06b6d4`/`#a855f7`,
  shared with `svgchart.go`) and the caution/critical states stay fixed —
  they are semantic and must not drift with the wallpaper. System colors
  carry no baked alpha: hairlines (e.g. the chart baseline) apply alpha in
  `ChartRenderer` code instead of in the colour resource.
  Radar cartography has a shared light/dark map palette for geographic
  readability, separate from the native wallpaper-derived panels.
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
- **Upstream fetches are cached, not rendered pages.** Rain providers use a
  two-minute memory cache. Open-Meteo response bodies use bounded memory and
  atomic disk caches under the XDG user cache directory (`weather/forecasts-v1`),
  with ten-minute hourly and thirty-minute daily TTLs. Reading from disk preserves
  the original expiry; relative forecasts expire at location midnight as well.
  Concurrent misses share one fetch. All Open-Meteo views and heatmaps share four
  request slots, 250 ms request spacing and a `Retry-After` cooldown after HTTP
  429; fresh cached responses remain available during that cooldown. Persistent
  responses retain the upstream timezone string for parsing after CLI restarts.
  Request pacing and in-flight deduplication are per process; separate running
  processes share stored responses but do not share a request limiter.
- **The ride heatmap is one batched fetch.** Open-Meteo accepts a list of
  coordinates per request but counts each location as one call against its
  free limit of 600 a minute, so a 21×21 grid costs 441 and grids are capped
  at 23×23 (529). `today` requests only
  precipitation and wind, about 200 locations per request (its proxy returns
  HTTP 414 near 450), with Unix times and hours relative to now; no request
  depends on the location's zone. The grid centres on the nearest point of a
  fixed lattice, so nearby positions reuse cached cells. Grid responses stay
  cached for 30 minutes. Web views wait at most five seconds, then show a
  retry note while the fetch keeps filling the cache; the CLI waits.
- **Calendar-day detail complements the ride heatmap.** `today --forecast`,
  `tomorrow` and `weekend` share the data and ride ranking used by `/day`,
  `/tomorrow`, `/weekend` and `/api/v1/outlook?period=today|tomorrow|weekend`.
  Weekend includes the current Saturday/Sunday when already in the weekend,
  with Sunday showing Sunday only. Windows use location midnight and calendar
  arithmetic so DST does not force a 24-hour day. The three views share one
  seven-day hourly response. Up to three non-overlapping two-hour ride windows
  are ranked by rain probability, wind, gusts, feels-like comfort and UV; each
  must be dry with rain chance at most 30%, wholly in daylight, start in the future, and have wind/gusts below
  the critical threshold. The Android widget opens `/day` when tapped.
- **Locations stay fixed across web views.** Navigation carries exact coordinates,
  the resolved display label and the original place-name input. Location forms
  preserve coordinate precision; editing a name clears coordinates, and editing
  coordinates clears the name and label. Invalid, incomplete and non-finite
  coordinates return HTTP 400; an explicit `(0, 0)` is valid. With no name or
  coordinates, the fallback is the server’s IP location. Forward and reverse
  geocoding share bounded 24-hour caches with in-flight deduplication.
- **One web radar player has Local / Netherlands coverage controls.** Local
  shows the latest available observation with the same labelled basemap as the
  widget. It is selected by default when fresh nearby rain is available;
  otherwise Netherlands is selected and Local is disabled. National frames
  load on demand once per page and survive coverage changes; switching views
  pauses playback. The widget’s `radar=play#radar-player` link plays the local
  forecast once; ordinary page visits keep the local observation paused.
- **Radar follows the system theme.** The web sends `theme=light|dark` from
  `prefers-color-scheme`; the widget sends its `uiMode` night setting. Local
  maps darken only the basemap and labels before compositing unchanged rain
  and the location marker. The embedded map fallback also has dark colours.
  National GIF/atlas rendering replaces only KNMI's known map-fill colours
  and footer caption ink on the supported 425×445 layout; weather colours,
  temperatures, forecast timestamps, frame offsets and disposal are retained.
  Observation detection runs on original frames before styling. Unknown
  national layouts remain unchanged. The original GIF stays cached separately
  from dark renders. Theme variants have separate bounded render-cache keys
  and reuse the same upstream weather and PDOK caches.
  Browser theme changes pause playback, preserve coverage/frame where possible,
  and reuse per-theme observations and frames; late responses cannot overwrite
  the active theme. The widget refreshes on application night-mode changes
  while its process is alive, with the existing periodic floor after process
  death. Android Force Dark is disabled on the map to preserve rain colours.
  The CLI can export either PNG or GIF with `weather radar --dark --output ...`.
- **Local playback uses a pinned forecast run.** `/api/v1/radar/local/timeline`
  starts with the actual latest observation, followed by KNMI
  `radar_forecast_2.0` frames every ten minutes (every second KNMI step)
  through the following hour, loaded in parallel. The
  `precipitation_nowcast` layer's `reference_time`, not its future default
  `time`, establishes freshness; every frame sends `DIM_REFERENCE_TIME`.
  Frames share the observation's projection, basemap, labels and exact marker.
  Upstream images deduplicate by rounded map cell, run and valid time, with
  a three-minute memory/disk cache and the shared WMS pacing/cooldown.
  Sixteen composed timelines stay cached for six minutes; their private
  `/radar/local-atlas.png` snapshots never enter the service worker cache.
  Forecast loading has a 45-second server budget and runs only after Play
  or a widget tap. Replays, scrubbing and coverage switches reuse frames.
  Playback stops at the end with Replay available. A coverage change or
  hidden page cancels pending autoplay. Failed or stale forecasts retain
  the current observation. The CLI exports the same sequence as a GIF with
  `weather radar --animate --output radar.gif`; the GIF plays once.
  Web requests add `progressive=1` to receive flushed NDJSON metadata, PNG
  frames and a final completion event. Playback starts after the observation
  and two forecast frames arrive, then waits without skipping if the next
  frame is delayed. Incomplete streams return to the observation. Rendered
  frames share a bounded six-minute cache with the CLI/legacy atlas, so replay
  and concurrent viewers reuse upstream fetches and rendering. Proxy buffering
  is disabled through `X-Accel-Buffering: no`; deployments must allow streaming.
- **KNMI radar opens paused on its newest supplied frame.** The national view uses
  a slider, Play/Pause, Now and Latest frame controls. Now selects the last
  observation. Playback stops at the last frame; Play at the end starts again
  from the first frame. The latest observation is
  identified by KNMI's black timestamp followed by red forecast
  timestamps on its 425×445 map. This visual convention is not an upstream API:
  Now is hidden if dimensions, caption colours or chronological ordering are
  unrecognised. `/api/v1/radar` describes the
  GIF's frame dimensions and delays; `/radar/atlas.png?id=...` serves a composed
  PNG atlas keyed by the GIF checksum and theme. Four snapshots stay cached for six minutes;
  GIF fetches keep their three-minute cache, so scrubbing never calls KNMI.
  Frame disposal and offsets are preserved, including the baked-in timestamp
  and legend. Past and future frames share the slider; timestamps are read from
  the image, never inferred. The original GIF is the fallback if controls fail.
- **Local widget radar is optional data.** `/api/v1/radar/local` returns a
  256×256 PNG of the latest KNMI WMS precipitation observation, with
  `X-Radar-Observed-At`; HTTP 204 means no nearby rain, stale data or unsupported
  coverage. The map spans 50 km in each direction in Web Mercator, with the
  precise location marked on a grey PDOK BRT basemap with town names, roads
  and water. A separate label layer sits above rain so town names stay readable.
  Embedded Natural Earth land is the fallback when tiles fail.
  PDOK tiles use shared in-flight deduplication and a seven-day memory/disk
  cache, with at most two network requests at once and a four-second map budget.
  Rain overlays and map tiles have separate lifetimes. WMS image
  requests share a three-minute cache on 0.01° cells; the marker keeps the exact
  requested coordinates. Raw WMS responses persist to disk for CLI reuse.
  Observation metadata has its own shared cache. WMS
  requests are spaced by one second anonymously or 50 ms with an API key,
  matching KNMI's 1/20 requests per second limits. Both have a 1,000-request
  hourly quota; the anonymous quota is shared. 403/429/5xx responses trigger a cooldown
  that honours Retry-After. `KNMI_WMS_API_KEY` selects authenticated WMS access;
  without it, anonymous shared quotas apply. Basemap failures retain the rain
  image over the embedded outline rather than hiding useful radar data.
  The widget first renders its existing layout, then adds the right-hand radar
  map only after a successful fresh image. Missing images, older servers,
  cached forecast fallback and missing location retain the original layout.
  Radar observations older than 20 minutes are rejected on both server and
  phone. `widget_rain_radar.xml` retains every measurement in native views,
  and requires at least 340dp width and 210dp height (scaled for larger fonts).
  Radar shows only the square map, with native bitmap corners matching the
  islands' 16dp radius and no surrounding panel or padding;
  provider, radius and timestamp labels are omitted.
  The CLI exports the same map with `weather radar --output radar.png`.
  Its chart width and native row height are budgeted separately in
  `chartSizePx`; radar taps open and play the web's local radar for the same location.
- **Detailed forecasts replace Hourly in web navigation.** Today, Tomorrow and
  Weekend provide the hourly tables and ride windows. The rolling `/hourly` URL
  and CLI command remain available for custom periods and existing links.
  Ride planning links are labelled `Ride: today` and `Ride: multiday`.
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
