# Forecast data

How upstream forecasts are cached and paced, the ride heatmap's batched fetch,
and the calendar-day views. Read it before changing `cmd/cache.go`,
`cmd/forecast_store.go`, `cmd/openmeteo_http.go`, `cmd/today*.go` or
`cmd/outlook.go`.

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
