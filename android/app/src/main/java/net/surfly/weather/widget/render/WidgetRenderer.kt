package net.surfly.weather.widget.render

import android.app.PendingIntent
import android.content.Context
import android.content.res.Configuration
import android.graphics.drawable.Icon
import android.util.SizeF
import android.util.TypedValue
import android.view.View
import android.widget.RemoteViews
import net.surfly.weather.widget.MapImages
import net.surfly.weather.widget.R
import net.surfly.weather.widget.net.GlanceResponse
import net.surfly.weather.widget.net.PointDto
import net.surfly.weather.widget.net.RadarMeta
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/** What the map slot shows. The slot itself never disappears for a given size. */
sealed interface MapState {
    /** A map from this refresh. */
    data class Live(val images: MapImages, val meta: RadarMeta) : MapState

    /** The last stored map, shown while the server can't be reached. */
    data class Stored(val images: MapImages, val meta: RadarMeta) : MapState

    /** The server has no usable map right now (stale observation). */
    data object Unavailable : MapState

    /** No map for this place or server (outside KNMI coverage, older server). */
    data object None : MapState

    /** Auto mode without a location fix. */
    data object NoLocation : MapState
}

sealed interface Lane {
    data class Chart(val data: ChartRenderer.Data) : Lane
    /** Dry: the hour strip and next rain when the server sends hours, else the radar and sunset rows. */
    data class Dry(
        val radar: String?,
        val sunset: String?,
        val hours: List<HourCell> = emptyList(),
        val nextRain: String? = null,
    ) : Lane
    data class Note(val text: String) : Lane
}

/** One column of the dry lane's hour strip. */
data class HourCell(val time: String, val icon: Int, val temp: String)

/** Tap targets; null for previews, which aren't interactive. */
class Links(val open: PendingIntent, val refresh: PendingIntent, val radar: PendingIntent?)

/** Everything the widget shows, independent of its size. */
class WidgetContent(
    val location: String,
    val pill: String,
    val response: GlanceResponse?,
    val laneTitle: String,
    val lane: Lane,
    val map: MapState,
    val links: Links?,
)

object WidgetRenderer {
    private val hhmm: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm")
    private val dayHhmm: DateTimeFormatter = DateTimeFormatter.ofPattern("EEE HH:mm", Locale.ENGLISH)
    private const val MIN_STRIP_HOURS = 3
    private val HOUR_IDS = listOf(
        listOf(R.id.hour1, R.id.hour1_time, R.id.hour1_icon, R.id.hour1_temp),
        listOf(R.id.hour2, R.id.hour2_time, R.id.hour2_icon, R.id.hour2_temp),
        listOf(R.id.hour3, R.id.hour3_time, R.id.hour3_icon, R.id.hour3_temp),
        listOf(R.id.hour4, R.id.hour4_time, R.id.hour4_icon, R.id.hour4_temp),
        listOf(R.id.hour5, R.id.hour5_time, R.id.hour5_icon, R.id.hour5_temp),
    )

    /** Builds one layout per size the launcher reported, so it never has to stretch one. */
    fun build(ctx: Context, content: WidgetContent, sizes: List<SizeF>): RemoteViews {
        val distinct = sizes.distinct().take(16).ifEmpty { listOf(SizeF(384f, 220f)) }
        // One Icon pair shared by every size, so the launcher holds each map once.
        val mapIcons = mapImages(content.map)?.let { Icon.createWithBitmap(it.light) to Icon.createWithBitmap(it.dark) }
        if (distinct.size == 1) return buildOne(ctx, content, distinct[0], mapIcons)
        return RemoteViews(distinct.associateWith { buildOne(ctx, content, it, mapIcons) })
    }

    private fun mapImages(state: MapState): MapImages? = when (state) {
        is MapState.Live -> state.images
        is MapState.Stored -> state.images
        else -> null
    }

    private fun buildOne(ctx: Context, c: WidgetContent, size: SizeF, mapIcons: Pair<Icon, Icon>?): RemoteViews {
        val plan = WidgetPlanner.plan(size.width, size.height, ctx.resources.configuration.fontScale)
        val row = plan.variant == Variant.ROW
        val v = RemoteViews(ctx.packageName, if (row) R.layout.widget_stacked_row else R.layout.widget_stacked)
        val r = c.response

        v.setTextViewText(R.id.location, c.location)
        v.setTextViewText(R.id.updated, c.pill)
        v.setImageViewResource(R.id.condition, conditionDrawable(r?.condition, r?.night == true))
        v.setTextViewText(R.id.temp_now, r?.let { "${it.temperature.now}°" } ?: "–°")
        v.setTextViewText(R.id.temp_end, r?.let { "${it.temperature.end}°" } ?: "–")
        if (!row) {
            v.setViewLayoutHeight(R.id.cond, plan.condDp, TypedValue.COMPLEX_UNIT_DIP)
            v.setViewVisibility(R.id.table, if (plan.showTable) View.VISIBLE else View.GONE)
            if (plan.showTable) populateTable(v, r)
        }

        populateLane(ctx, v, c, plan)
        populateMap(ctx, v, c, plan, mapIcons)

        c.links?.let { links ->
            v.setOnClickPendingIntent(android.R.id.background, links.open)
            v.setOnClickPendingIntent(R.id.updated, links.refresh)
            if (plan.mapDp > 0f && links.radar != null && mapIcons != null) {
                v.setOnClickPendingIntent(R.id.map_box, links.radar)
            }
        }
        return v
    }

    private fun populateTable(v: RemoteViews, r: GlanceResponse?) {
        if (r == null) {
            listOf(R.id.feels_now, R.id.feels_end, R.id.wind_now, R.id.wind_end, R.id.uv_now, R.id.uv_end)
                .forEach { v.setTextViewText(it, "–") }
            return
        }
        v.setTextViewText(R.id.feels_now, "${r.feelsLike.now}°")
        v.setTextViewText(R.id.feels_end, "${r.feelsLike.end}°")
        v.setTextViewText(R.id.wind_now, "${windArrow(r.wind.now.directionDeg)}${r.wind.now.speedKmh}")
        v.setTextViewText(R.id.wind_end, "${windArrow(r.wind.end.directionDeg)}${r.wind.end.speedKmh}")
        v.setTextViewText(R.id.uv_now, "${r.uvIndex.now}")
        v.setTextViewText(R.id.uv_end, "${r.uvIndex.end}")
        // Resolved by the launcher in its current theme, so day/night switches stay correct.
        warningColor(windLevel(r.wind.now.speedKmh))?.let { v.setColorStateList(R.id.wind_now, "setTextColor", it) }
        warningColor(windLevel(r.wind.end.speedKmh))?.let { v.setColorStateList(R.id.wind_end, "setTextColor", it) }
        warningColor(uvLevel(r.uvIndex.now))?.let { v.setColorStateList(R.id.uv_now, "setTextColor", it) }
        warningColor(uvLevel(r.uvIndex.end))?.let { v.setColorStateList(R.id.uv_end, "setTextColor", it) }
    }

    private fun populateLane(ctx: Context, v: RemoteViews, c: WidgetContent, plan: WidgetPlan) {
        v.setTextViewText(R.id.lane_title, c.laneTitle)
        var chart = View.GONE
        var dry = View.GONE
        var strip = View.GONE
        var note = View.GONE
        when (val lane = c.lane) {
            is Lane.Chart -> {
                val density = ctx.resources.displayMetrics.density
                val w = (plan.chartWidthDp * density).toInt()
                val h = (plan.chartHeightDp * density).toInt()
                if (w >= 64 && h >= 32) {
                    val light = ChartRenderer.render(themed(ctx, night = false), w, h, lane.data)
                    val dark = ChartRenderer.render(themed(ctx, night = true), w, h, lane.data)
                    v.setIcon(R.id.chart, "setImageIcon", Icon.createWithBitmap(light), Icon.createWithBitmap(dark))
                    chart = View.VISIBLE
                }
            }
            is Lane.Dry -> if (lane.hours.size >= MIN_STRIP_HOURS) {
                strip = View.VISIBLE
                populateStrip(v, lane)
            } else {
                dry = View.VISIBLE
                v.setViewVisibility(R.id.dry_radar_row, if (lane.radar != null) View.VISIBLE else View.GONE)
                lane.radar?.let { v.setTextViewText(R.id.dry_radar, it) }
                v.setViewVisibility(R.id.dry_sunset_row, if (lane.sunset != null) View.VISIBLE else View.GONE)
                lane.sunset?.let { v.setTextViewText(R.id.dry_sunset, it) }
            }
            is Lane.Note -> {
                note = View.VISIBLE
                v.setTextViewText(R.id.lane_note, lane.text)
            }
        }
        v.setViewVisibility(R.id.chart, chart)
        v.setViewVisibility(R.id.dry_rows, dry)
        v.setViewVisibility(R.id.dry_strip, strip)
        v.setViewVisibility(R.id.lane_note, note)
    }

    private fun populateStrip(v: RemoteViews, lane: Lane.Dry) {
        HOUR_IDS.forEachIndexed { i, (cell, time, icon, temp) ->
            val hour = lane.hours.getOrNull(i)
            v.setViewVisibility(cell, if (hour != null) View.VISIBLE else View.INVISIBLE)
            if (hour != null) {
                v.setTextViewText(time, hour.time)
                v.setImageViewResource(icon, hour.icon)
                v.setTextViewText(temp, hour.temp)
            }
        }
        v.setTextViewText(R.id.dry_next_rain, lane.nextRain ?: "")
        v.setViewVisibility(R.id.dry_strip_sunset, if (lane.sunset != null) View.VISIBLE else View.GONE)
        lane.sunset?.let { v.setTextViewText(R.id.dry_strip_sunset, it) }
    }

    private fun populateMap(ctx: Context, v: RemoteViews, c: WidgetContent, plan: WidgetPlan, icons: Pair<Icon, Icon>?) {
        if (plan.mapDp <= 0f) {
            v.setViewVisibility(R.id.map_box, View.GONE)
            return
        }
        val row = plan.variant == Variant.ROW
        v.setViewVisibility(R.id.map_box, View.VISIBLE)
        v.setViewLayoutWidth(R.id.map_box, plan.mapDp, TypedValue.COMPLEX_UNIT_DIP)
        if (!row) v.setViewLayoutHeight(R.id.map_box, plan.mapDp, TypedValue.COMPLEX_UNIT_DIP)

        if (icons != null) {
            v.setIcon(R.id.map, "setImageIcon", icons.first, icons.second)
            v.setViewVisibility(R.id.map, View.VISIBLE)
            v.setViewVisibility(R.id.map_placeholder, View.GONE)
        } else {
            v.setViewVisibility(R.id.map, View.GONE)
            v.setViewVisibility(R.id.map_placeholder, View.VISIBLE)
            v.setTextViewText(
                R.id.map_placeholder_text,
                ctx.getString(
                    when (c.map) {
                        MapState.NoLocation -> R.string.map_no_location
                        MapState.Unavailable -> R.string.map_unavailable
                        else -> R.string.map_none
                    },
                ),
            )
        }

        val meta = when (val m = c.map) {
            is MapState.Live -> m.meta
            is MapState.Stored -> m.meta
            else -> null
        }
        val note = when {
            meta == null -> null
            c.map is MapState.Stored -> meta.observedAt?.let { ctx.getString(R.string.map_stored, clock(it)) }
                ?: ctx.getString(if (row) R.string.map_unavailable_short else R.string.map_unavailable)
            meta.unavailable -> ctx.getString(if (row) R.string.map_unavailable_short else R.string.map_unavailable)
            meta.outline && !row -> ctx.getString(R.string.map_outline)
            else -> null
        }
        v.setViewVisibility(R.id.map_note, if (note != null) View.VISIBLE else View.GONE)
        note?.let { v.setTextViewText(R.id.map_note, it) }
        val playable = !row && c.links?.radar != null && c.map is MapState.Live && meta?.unavailable == false
        v.setViewVisibility(R.id.map_play, if (playable) View.VISIBLE else View.GONE)
    }

    /** A context resolving resources in light or dark mode, for drawing both chart versions. */
    fun themed(ctx: Context, night: Boolean): Context {
        val cfg = Configuration(ctx.resources.configuration)
        cfg.uiMode = (cfg.uiMode and Configuration.UI_MODE_NIGHT_MASK.inv()) or
            if (night) Configuration.UI_MODE_NIGHT_YES else Configuration.UI_MODE_NIGHT_NO
        return ctx.createConfigurationContext(cfg)
    }

    // ── lane content ──

    /** Lane title and contents for a forecast; the map state supplies the dry radar note. */
    fun lane(ctx: Context, r: GlanceResponse, map: MapState): Pair<String, Lane> {
        val alarm = r.buienalarm?.data.orEmpty()
        val radar = r.buienradar?.data.orEmpty()
        val message = r.buienalarm?.desc?.takeIf { it.isNotBlank() && it != "Buienalarm" }
        if (!ChartRenderer.hasNowcast(alarm, radar)) {
            return ctx.getString(R.string.state_no_data) to Lane.Note(ctx.getString(R.string.note_no_nowcast))
        }
        if (ChartRenderer.isDryWindow(alarm, radar)) {
            val hours = r.hours.mapNotNull { h ->
                val at = runCatching { OffsetDateTime.parse(h.time) }.getOrNull() ?: return@mapNotNull null
                HourCell(at.format(hhmm), conditionDrawable(h.condition, h.night), "${h.temperature}°")
            }
            return (message ?: ctx.getString(R.string.dry_fallback)) to
                Lane.Dry(dryRadarNote(ctx, map), sunset(r), hours, nextRainLabel(ctx, r))
        }
        return (message ?: peakLabel(alarm, radar)) to Lane.Chart(chartData(r, message))
    }

    private fun dryRadarNote(ctx: Context, map: MapState): String? {
        val meta = when (map) {
            is MapState.Live -> map.meta
            is MapState.Stored -> map.meta
            else -> return null
        }
        return when {
            meta.unavailable -> ctx.getString(R.string.map_unavailable)
            meta.rainNearby == true -> ctx.getString(R.string.dry_radar_nearby)
            meta.rainNearby == false -> ctx.getString(R.string.dry_radar_none)
            else -> null
        }
    }

    /** "Next rain 21:00" today, "Next rain Thu 07:00" on a later day; null from older servers. */
    private fun nextRainLabel(ctx: Context, r: GlanceResponse): String? {
        val first = r.hours.firstOrNull()?.let { runCatching { OffsetDateTime.parse(it.time) }.getOrNull() } ?: return null
        val next = r.nextRain?.let { runCatching { OffsetDateTime.parse(it.time) }.getOrNull() }
            ?: return ctx.getString(R.string.dry_no_rain)
        // The first strip hour is the next full hour, so one hour earlier is "today".
        val sameDay = next.toLocalDate() == first.minusHours(1).toLocalDate()
        return ctx.getString(R.string.dry_next_rain, next.format(if (sameDay) hhmm else dayHhmm))
    }

    private fun sunset(r: GlanceResponse): String? =
        r.sunset?.let { runCatching { OffsetDateTime.parse(it) }.getOrNull() }?.format(hhmm)

    private fun chartData(r: GlanceResponse, message: String?): ChartRenderer.Data = ChartRenderer.Data(
        buienalarm = r.buienalarm?.data.orEmpty(),
        buienradar = r.buienradar?.data.orEmpty(),
        tempNow = r.temperature.now,
        tempEnd = r.temperature.end,
        conditionLabel = r.condition,
        sunEvents = r.sun.mapNotNull { ev ->
            runCatching { ChartRenderer.SunEvent(ev.kind, OffsetDateTime.parse(ev.time).toInstant()) }.getOrNull()
        },
        dryHeadline = message,
    )

    /** "max 1.4 mm/h · moderate": the rain peak across the chart window. */
    private fun peakLabel(alarm: List<PointDto>, radar: List<PointDto>): String {
        val peak = maxOf(alarm.maxOfOrNull { it.value } ?: 0.0, radar.maxOfOrNull { it.value } ?: 0.0)
        val intensity = when {
            peak < 0.5 -> "drizzle"
            peak < 2.0 -> "moderate"
            peak < 5.0 -> "heavy"
            else -> "downpour"
        }
        return "max %.1f mm/h · %s".format(peak, intensity)
    }

    // ── shared helpers ──

    private enum class Level { NORMAL, CAUTION, CRITICAL }

    // Thresholds mirror serve_glance.go.
    private fun windLevel(kmh: Int) = when {
        kmh >= 50 -> Level.CRITICAL
        kmh >= 28 -> Level.CAUTION
        else -> Level.NORMAL
    }

    private fun uvLevel(uv: Int) = when {
        uv >= 8 -> Level.CRITICAL
        uv >= 3 -> Level.CAUTION
        else -> Level.NORMAL
    }

    private fun warningColor(level: Level): Int? = when (level) {
        Level.CRITICAL -> R.color.chart_critical
        Level.CAUTION -> R.color.chart_caution
        Level.NORMAL -> null
    }

    /** Wind FROM `dirFromN` degrees → arrow showing where it's blowing TO. */
    fun windArrow(dirFromN: Int): String {
        val toDeg = ((dirFromN + 180) % 360 + 360) % 360
        return arrayOf("↑", "↗", "→", "↘", "↓", "↙", "←", "↖")[((toDeg + 22) / 45) % 8]
    }

    /** At night, clear shows a moon and partly cloudy shows plain cloud, like the glance page. */
    fun conditionDrawable(token: String?, night: Boolean = false): Int = when {
        night && token == "partly_cloudy" -> R.drawable.ic_cond_overcast
        night && (token == null || token == "clear") -> R.drawable.ic_cond_night
        else -> dayDrawable(token)
    }

    private fun dayDrawable(token: String?): Int = when (token) {
        "partly_cloudy" -> R.drawable.ic_cond_partly_cloudy
        "overcast" -> R.drawable.ic_cond_overcast
        "fog" -> R.drawable.ic_cond_fog
        "drizzle" -> R.drawable.ic_cond_drizzle
        "rain" -> R.drawable.ic_cond_rain
        "snow" -> R.drawable.ic_cond_snow
        "thunderstorm" -> R.drawable.ic_cond_thunderstorm
        else -> R.drawable.ic_cond_clear
    }

    fun clock(instant: Instant): String = instant.atZone(ZoneId.systemDefault()).format(hhmm)
}
