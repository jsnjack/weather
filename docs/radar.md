# Radar

The web radar player, the widget map and the CLI export: endpoints, theming,
KNMI forecast runs, caches and rate limits. Read it before changing
`cmd/serve_radar_*.go`, `cmd/cmd_radar.go` or `cmd/web/radar.js`.

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
