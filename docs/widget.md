# The Android widget

How the home-screen widget lays itself out and takes its colours. Read it before
changing `android/`. [AGENTS.md](../AGENTS.md) has the short rules, and
[android/README.md](../android/README.md) the build and iterate workflow.

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
  "Dry for the next 2 hours"), and below it the dual-provider chart (rain), an
  hour strip (dry) or a note (no nowcast, no location, error). The strip shows
  the next five hours (time, sky with a moon after dark, temperature) from
  `/api/v1/glance`'s `hours`, then "Next rain …" from `next_rain` (the first
  Open-Meteo hour from now+2h with ≥0.1 mm; amounts only, never probability)
  and the sunset; servers without `hours` get the older RADAR and SUNSET rows.
  The glance API's `night` flag gives the hero icon its moon. Each
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
