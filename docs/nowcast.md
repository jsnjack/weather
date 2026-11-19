# The 2-hour nowcast

How the two lines on the 2-hour rain chart are sourced, at home and abroad.
Read it before changing `cmd/forecast_*.go` or `fetchRain`.

- **Line 1 is Buienalarm everywhere.** Its API answers for any point, from
  three products named in `summary.source`: `radar-nl`, `radar-westeurope`
  (Germany, western Austria) and `radar-world` for everything else. The
  worldwide product's newest image is about 30 minutes old, updates every 10
  minutes and reports rain over open ocean where radar sees none, so it blends
  in non-radar data. Infoplaza documents none of it. Series from
  `radar-world` carry `estimate: true` and are labelled
  "Buienalarm (estimate)" in chart keys.
- **Line 2 comes from the national radar for the country.** `getSecondNowcast`
  takes the country from reverse geocoding (the same cached lookup as the
  location's name) and tries `secondNowcastSources` in order:

  | Country | Source | Step / ahead | Notes |
  |---|---|---|---|
  | NL, BE, LU | Buienradar | 5 min / 3 h | `graphdata.buienradar.nl`; 404 outside NL, BE and western Germany |
  | DE | DWD via Bright Sky, then Buienradar | 5 min / 2 h | `date=now` returns the latest run to +2 h; values are 0.01 mm per 5 min; 0 outside coverage, so only for Germany |
  | AT | GeoSphere Austria | 15 min / 2.5 h | INCA (radar, stations, model, terrain); 20–25 min late; 240 requests an hour |
  | DK, NO, SE, FI | MET Norway | 5 min / 1h50 | `meta.radar_coverage` must be `ok`; identifying User-Agent, ≤4 decimals |
  | other, inside 45.5–49.48°N, 8.1–17.74°E | GeoSphere Austria | 15 min / 2.5 h | its grid reaches into CH, LI, IT, SI, CZ, SK, HU |
  | unknown (geocoding failed) | Buienradar, plus GeoSphere inside its grid | | the old behaviour |
  | anywhere else | none | | line 1 alone |

  The JSON keeps the misspelled `buineradar` key for line 2, so widgets that
  predate the chain still draw it, and adds `source` (and `estimate`) to every
  series. Web keys, the CLI legend and the glance footer show the source; the
  footer's credits satisfy the CC BY 4.0 licences of DWD, GeoSphere Austria
  and MET Norway.
- **No model line where radar is missing.** Belarus, Poland, the Baltics, the
  UK, southern Europe and the rest of the world have no open radar-based point
  nowcast (checked November 2026). The options there were OpenWeatherMap's
  minute forecast (key, model, one hour) and Open-Meteo `minutely_15`, which
  outside central Europe and North America is the hourly value split into
  quarters. The user chose no second line rather than a model one: a radar
  line and a model line disagree for reasons unrelated to the weather.
- **France is possible but skipped.** Météo-France's PIAF radar nowcast
  (5 min to +3 h, area grids) needs a free account and token; add it as a
  source for `FR` if that changes.
- **Caching.** Each source has its own TTL cache keyed by rounded coordinates:
  Buienradar and DWD two minutes, GeoSphere five (runs are 15 minutes apart)
  and MET Norway five (at least its `Expires`). Errors are never cached, and a
  failed source falls through to the next one in the list.
- **Slow sources don't hold up the page.** GeoSphere takes 5–20 seconds when
  its own server has the point uncached (about 1 second when cached; requests
  are rounded to its 1 km grid so nearby points share that cache). A refresh
  waits for a source at most 6 seconds, or 1.5 seconds when it has an answer
  from the last 30 minutes; then it shows that answer trimmed to the future,
  or no second line, and the fetch finishes in the background for the next
  refresh (`fetchSecondNowcast`).
