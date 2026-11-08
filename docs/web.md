# The web pages

The design language the web pages share with the widget, and the glance page.
Read it before changing `cmd/web/` or `cmd/serve_glance*.go`.

- **One design language, two colour sources.** Web and widget share the same
  structure — islands on a card (14–16px radii), an accent island for the NOW
  hero, labelled now/+2h stats, the nowcast message as quiet text (a centred
  caption on the web, the rain lane's first line on the widget), pill-shaped
  meta controls, and one line-icon set for sky conditions: the widget's
  `ic_cond_*.xml` vector drawables, rendered on the web as inline SVG in the
  text colour (`condition_icons.go`; `TestConditionIconsMatchWidget` fails if
  the two copies drift, so change the XML and regenerate the table together).
  Clear and partly cloudy hours after dark show the moon and plain cloud on
  both. The **web is warm paper** (fixed
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
