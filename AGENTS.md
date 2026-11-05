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
  serve_radar_outline.go          Outline of KNMI's 30-minute rain forecast on the local map
  serve_radar_theme.go            Dark basemap/label styling that preserves weather colours
  outlook.go                     Calendar-day windows and ranked dry ride windows
  serve_outlook.go               /day, /tomorrow, /weekend and /api/v1/outlook
  serve_glance.go                Unified glance payload (rain + Open-Meteo snapshot)
  serve_glance_page.go           /glance: now, 2 hours, today and 13 days on one time axis
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
- **The widget has one fixed layout per size (Stacked).** `WidgetPlanner`
  picks the layout from the launcher's exact sizes (`OPTION_APPWIDGET_SIZES`,
  one `RemoteViews` per size through the size map), never from the weather.
  FULL (`widget_stacked.xml`): a header with the location and the refresh
  pill; on the left an accent conditions panel (condition icon, big light NOW
  temperature, warm +2H, and a feels/wind/UV table under a **NOW / +2H column
  header** — two unlabelled columns read as gibberish) above a neutral rain
  lane; on the right a square map. PANEL drops the map when the map would be
  under 110 dp; ROW (`widget_stacked_row.xml`) puts everything in one row for
  short 4×1 widgets. Rain and dry fill the same boxes: the lane's first line
  is always the nowcast sentence (Buienalarm's message, else the peak label or
  "Dry for the next 2 hours"), and below it the dual-provider chart (rain), the
  RADAR and SUNSET rows (dry) or a note (no nowcast, no location, error). Each
  refresh calls `updateAppWidget` **once**, after the forecast and both map
  themes have loaded. "Refreshing…", resizes (`KEY_RENDER_ONLY`) and offline
  fallbacks redraw from `WidgetStore` (last forecast JSON and last light/dark
  map) in the same layout. The previous design drew the widget without the map
  and then again with it, and the map came and went with rain within 50 km;
  that flicker is why the slot is permanent. The chart bitmap is drawn at the
  lane's exact size (`WidgetPlan`), so `fitXY` has nothing to stretch, and the
  renderer skips any 30-minute tick whose label would touch another; all other
  text is native (bitmap text squishes, which is why bitmap-drawn dry heroes
  were rejected). Hard rules learned by rejection: **no bottom data strip**
  (flat dry data reads as a fake progress bar) and **no provider names** in the
  dry state. **Dry vs rainy is decided once, on the capped chart window**
  (`chartWindow` in `ChartRenderer.kt`): `isDryWindow` and `render` must judge
  identical data. Corners use the launcher's
  `system_app_widget_background_radius` and `system_app_widget_inner_radius`.
  The root is `@android:id/background` for the launch animation. Settings are
  a Material 3 screen (`ConfigureActivity`) with a preview drawn from the
  store; the widget is `reconfigurable|configuration_optional`, so long-press →
  Settings changes it in place. After a refresh, at most every six hours, the
  widget sends its current rendering to the picker (`setWidgetPreview`).
- **One design language, two colour sources.** Web and widget share the same
  structure — islands on a card (14–16px radii), an accent island for the NOW
  hero, labelled now/+2h stats, the nowcast message as quiet text (a centred
  caption on the web, the rain lane's first line on the widget), pill-shaped
  meta controls. The **web is warm paper** (fixed
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
  `ChartRenderer` code instead of in the colour resource, and the map chips'
  scrim is a colour-state list with alpha. **Every update carries both
  themes**: the map and chart go through `RemoteViews.setIcon(…, light, dark)`,
  warning colours through `setColorStateList(…, @ColorRes)`, and icon tints
  are `android:tint` in the layout (launchers ignore `app:tint`), so the
  launcher switches immediately without starting the app. A single bitmap
  stayed light after One UI switched to dark mode while the process was dead.
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
  seven-day hourly response. Hours that have already ended are hidden; the
  current hour stays. Up to three non-overlapping two-hour ride windows
  are ranked by rain probability, wind, gusts, feels-like comfort and UV; each
  must be dry with rain chance at most 30%, wholly in daylight, start in the future, and have wind/gusts below
  the critical threshold. The Android widget opens `/glance` when tapped, and
  its map plays the local radar there (`radar=play#radar-player`).
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
  `prefers-color-scheme`; the widget requests both themes on every refresh. Local
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
  the active theme. The widget needs no refresh on a theme change, because
  each update carries both images. Android Force Dark is disabled on the map
  to preserve rain colours.
  The CLI can export either PNG or GIF with `weather radar --dark --output ...`.
- **Local playback uses a pinned forecast run.** `/api/v1/radar/local/timeline`
  starts with the actual latest observation, followed by KNMI
  `radar_forecast_2.0` frames every ten minutes (every second KNMI step) for
  two hours, capped where the run ends (two hours after its reference time),
  loaded in parallel. The
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
- **The widget map is always shown.** `/api/v1/radar/local` returns a PNG of
  the latest KNMI WMS precipitation observation with `X-Radar-Observed-At`
  and `X-Radar-Rain: nearby|none`. Without options, HTTP 204 means no rain in
  the 50 km circle or stale data, and both theme requests behave as before for
  the web and older widgets. The widget sends `always=1&size=512&outline=30`:
  `always=1` returns the dry map instead of 204, and when the observation is
  stale or KNMI fails it returns the map without rain with
  `X-Radar-Status: unavailable` (no observation time); `size=512` renders a
  512×512 map of the same area, with the background one PDOK zoom higher,
  labels enlarged from the 256 map's zoom (Catmull-Rom) so they keep their
  size, rain cells nearest-neighbour and the marker doubled; `outline=30` draws
  the outline of where KNMI's pinned nowcast run expects rain 30 minutes after
  the observation (`serve_radar_outline.go`: blurred forecast mask, anti-aliased
  iso-line with a halo, above the rain and below the labels), sending
  `X-Radar-Outline: 30` only when a line was drawn. The outline waits at most
  three seconds for the forecast frame; a slow, failed, empty or out-of-run
  forecast never delays or fails the map. Coverage outside KNMI's composite is
  still 204; invalid options are 400. The map spans 50 km in each direction in
  Web Mercator, with the precise location marked on a grey PDOK BRT basemap
  with town names, roads and water. A separate label layer sits above rain so
  town names stay readable. Embedded Natural Earth land is the fallback when
  tiles fail.
  The web map zooms between 25, 50 and 100 km (`radius=` on the local
  image and timeline endpoints; `weather radar --radius`); the widget stays
  at 50 km. Only the 50 km map requires rain inside its circle; zoomed maps
  may be dry because the web offers zoom only after that map appeared.
  Each KNMI request covers 100 km at 512×512, and every zoom and size crops its
  centre with nearest-neighbour sampling, so zooming adds no KNMI requests.
  PDOK leaves the sea transparent below zoom 8, so the 100 km background
  uses zoom 8 tiles at half size; its labels stay at zoom 7.
  PDOK tiles use shared in-flight deduplication and a seven-day memory/disk
  cache, with at most two network requests at once and a four-second map budget.
  Composed background and label layers are cached for 30 minutes independent
  of the observation; rendered maps for three minutes per place, radius, size,
  theme, observation and forecast run. The widget's parallel light and dark
  requests share every upstream fetch. WMS image
  requests share a three-minute cache on 0.01° cells; the marker keeps the exact
  requested coordinates. Raw WMS responses persist to disk for CLI reuse.
  Observation metadata has its own shared cache. WMS
  requests are spaced by one second anonymously or 50 ms with an API key,
  matching KNMI's 1/20 requests per second limits. Both have a 1,000-request
  hourly quota; the anonymous quota is shared. 403/429/5xx responses trigger a cooldown
  that honours Retry-After. `KNMI_WMS_API_KEY` selects authenticated WMS access;
  without it, anonymous shared quotas apply. Basemap failures retain the rain
  image over the embedded outline rather than hiding useful radar data.
  Radar observations older than 20 minutes are rejected on both server and
  phone (`RadarPolicy`); the phone shows "Radar unavailable" instead. Offline,
  the widget shows its last stored map near the same place (within 2 km)
  with a "Radar HH:mm" chip. KNMI's `radar/nearest` classes are recoloured
  after decoding (`localRadarRainColors`): KNMI draws light rain white and
  grey, which vanished on the light basemap. Observation and forecast frames,
  web, widget and CLI share the sky-blue → magenta scale in both themes.
  On the widget the map has no surrounding panel; chips mark the outline
  ("Line: in 30 min"), an unavailable or stored map, and "▶ 2 h". Map taps
  open and play the web's local radar for the same location. The CLI exports
  the same map with `weather radar --output radar.png [--size 512] [--outline]`.
- **Detailed forecasts replace Hourly in web navigation.** Glance comes first,
  then Today, Tomorrow and Weekend provide the hourly tables and ride windows.
  The rolling `/hourly` URL and CLI command remain available for custom
  periods and existing links. Ride planning links are labelled `Ride: today`
  and `Ride: multiday`.
- **The glance page puts every horizon on one time axis.** `/glance` is the
  widget's tap target and takes the same location parameters as the other
  pages. One rail carries rows of 5 minutes (both nowcast providers drawn down
  the axis in mm/h, capped at the Buienalarm horizon, with labelled rain-start,
  per-provider peak and dry-again events and a text alternative), then 1 hour
  for the rest of today from where the nowcast ends (the `/day` outlook, with
  night shading and sun times), then 1 day for the next 13 days (the
  `/forecast` daily request). Header readouts come from `buildGlanceResponse`.
  Hours and days share one °C scale, so daily low–high bars line up with the
  hourly dots. In hourly and daily rows the rain cell follows the sky icon:
  the chance in bold, in the water colour from 40% or from 1 mm (Open-Meteo's
  daily chance can stay under 30% on a 17 mm day), with the amount or "dry"
  under it; the 5-minute rows never show Open-Meteo's rain chance. Today's
  ranked ride windows sit in the hourly band; when none are left, tomorrow's
  sit under tomorrow's row. The page streams like the others, loads its three
  sources in parallel through the existing caches (one shared hourly request)
  and gives each band its own note when its source fails. The local radar
  player sits beside the 5-minute rows from 760 px and below them on phones;
  `data-outline="30"` makes `radar.js` request `outline=30` and show an
  "in 30 min" key only when the server drew a line. `glance_body` repeats the
  index player markup, so change both with `radar.js`. There is no CLI
  command; the root chart, `today --forecast` and `forecast` print the same
  data.
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
