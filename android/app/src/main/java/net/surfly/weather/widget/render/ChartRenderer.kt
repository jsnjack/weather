package net.surfly.weather.widget.render

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Path
import android.graphics.Rect
import android.graphics.RectF
import android.util.TypedValue
import androidx.core.content.ContextCompat
import net.surfly.weather.widget.R
import net.surfly.weather.widget.net.PointDto
import java.time.Instant
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import kotlin.math.max

/**
 * Renders the widget chart: rainy state (two provider lines with framing
 * temperature numbers), or a quiet dry state with the same provider horizon
 * compressed into a minimal strip.
 */
object ChartRenderer {

    private const val DRY_THRESHOLD = 0.05 // mm/h — both providers under this == dry
    private val hhmm: DateTimeFormatter = DateTimeFormatter.ofPattern("HH:mm")

    data class Data(
        val buienalarm: List<PointDto>,
        val buienradar: List<PointDto>,
        val tempNow: Int,
        val tempEnd: Int,
        val conditionLabel: String,
        val microNow: Micro? = null,
        val microEnd: Micro? = null,
        val sunEvents: List<SunEvent> = emptyList(),
        /** Headline for the dry hero — usually the Buienalarm nowcast
         *  message ("It will stay dry for now"); falls back to a generic
         *  "Dry for the next 2 hours" when no message is available. */
        val dryHeadline: String? = null,
    )

    /** Structured micro stats so the renderer can colourise individual
     *  parts (wind / UV) based on caution thresholds. */
    data class Micro(
        val feels: Int,
        val windArrow: String,
        val windKmh: Int,
        val uv: Int,
    )

    data class SunEvent(val kind: String, val time: Instant)

    /** True when both providers stay under the dry threshold across the
     *  visible chart window — i.e. the dry surface (not the chart) will be
     *  rendered. Exposed so callers can swap surrounding UI accordingly
     *  (e.g. blank the peak TextView so it doesn't duplicate the dry
     *  headline). Must judge the same capped window as [render]: radar rain
     *  beyond the Buienalarm horizon is invisible on the chart, so counting
     *  it here would split the two decisions — the caller would show the
     *  rainy chrome around a hero bitmap. */
    fun isDryWindow(alarm: List<PointDto>, radar: List<PointDto>): Boolean {
        val (alarmParsed, radarCapped) = chartWindow(alarm, radar)
        val peak = maxOf(
            alarmParsed.maxOfOrNull { it.second } ?: 0.0,
            radarCapped.maxOfOrNull { it.second } ?: 0.0,
        )
        return peak < DRY_THRESHOLD
    }

    /** Parses both series and caps radar to Buienalarm's horizon — the
     *  window the chart actually shows. Buienalarm is the shorter (and
     *  authoritative) nowcast window. Shared by [isDryWindow] and [render]
     *  so the dry/rainy decision is made on identical data. */
    private fun chartWindow(
        alarmDto: List<PointDto>,
        radarDto: List<PointDto>,
    ): Pair<List<Pair<Instant, Double>>, List<Pair<Instant, Double>>> {
        val alarm = parse(alarmDto)
        val radarRaw = parse(radarDto)
        val alarmLast = alarm.lastOrNull()?.first
        val radar = if (alarmLast != null) radarRaw.filter { !it.first.isAfter(alarmLast) } else radarRaw
        return alarm to radar
    }

    /** True when at least one provider returned points. Lets callers tell a
     *  genuine "no nowcast data" state apart from a dry window (data present
     *  but all under the threshold) — the two look identical to [isDryWindow]
     *  otherwise, since an empty list also peaks at 0. */
    fun hasNowcast(alarm: List<PointDto>, radar: List<PointDto>): Boolean =
        alarm.isNotEmpty() || radar.isNotEmpty()

    fun render(context: Context, widthPx: Int, heightPx: Int, data: Data): Bitmap {
        val bmp = Bitmap.createBitmap(widthPx, heightPx, Bitmap.Config.ARGB_8888)
        val canvas = Canvas(bmp)

        val (alarm, radar) = chartWindow(data.buienalarm, data.buienradar)
        // No points from either provider is a distinct state from "dry": show
        // an explicit message rather than the dry hero, which would imply we
        // know it's dry when we actually have no nowcast at all. (When alarm
        // is empty, chartWindow leaves radar uncapped, so this check still
        // sees every radar point.)
        if (alarm.isEmpty() && radar.isEmpty()) {
            drawMessage(context, canvas, widthPx, heightPx, context.getString(R.string.state_no_data))
            return bmp
        }

        val maxRain = max(
            alarm.maxOfOrNull { it.second } ?: 0.0,
            radar.maxOfOrNull { it.second } ?: 0.0,
        )

        if (maxRain < DRY_THRESHOLD) {
            drawHero(context, canvas, widthPx, heightPx, data)
        } else {
            drawRainy(context, canvas, widthPx, heightPx, alarm, radar, data, maxRain)
        }
        return bmp
    }

    // ─────────────── rainy state ───────────────
    private fun drawRainy(
        context: Context,
        canvas: Canvas,
        w: Int,
        h: Int,
        alarm: List<Pair<Instant, Double>>,
        radar: List<Pair<Instant, Double>>,
        data: Data,
        maxRain: Double,
    ) {
        val density = context.resources.displayMetrics.density

        // padLeft needs to fit the leftmost HH:mm time label, which is
        // anchored LEFT at xNow == plotL. 12dp left only the left half of
        // the leading digit visible after fitXY stretching; 22dp puts the
        // whole timestamp clear of the chart's left edge.
        val padLeft = dp(22f, density)
        val padRight = dp(22f, density)
        val padTop = dp(8f, density)
        // Bottom area carries only the HH:mm time row — temps and micro
        // stats are native TextViews below the chart island (bitmap text
        // goes soft under fitXY; the same reason the dry state is native).
        val padBottom = dp(20f, density)

        val plotL = padLeft
        val plotR = w - padRight
        val plotT = padTop
        val plotB = h - padBottom
        val plotW = (plotR - plotL).coerceAtLeast(1f)
        val plotH = (plotB - plotT).coerceAtLeast(1f)

        // X range: earliest data point → alarm's last forecast point.
        // Radar is already clipped to alarm horizon in render().
        val allPoints = alarm + radar
        val tMin = allPoints.minOf { it.first }
        val tMax = alarm.lastOrNull()?.first ?: allPoints.maxOf { it.first }

        val nowInstant = Instant.now()
        val xMinMs = tMin.toEpochMilli().toDouble()
        val xMaxMs = tMax.toEpochMilli().toDouble()
        val xSpan = (xMaxMs - xMinMs).coerceAtLeast(1.0)
        fun xOf(t: Instant): Float =
            plotL + ((t.toEpochMilli() - xMinMs) / xSpan).toFloat() * plotW

        // Y range: snug fit to actual peak with 15% headroom; floor at
        // 0.5 mm/h so a near-zero drizzle doesn't show as full-bleed.
        val yHi = max(0.5, maxRain * 1.15)
        fun yOf(v: Double): Float =
            plotB - (v / yHi).toFloat().coerceIn(0f, 1f) * plotH

        val gridPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = ContextCompat.getColor(context, R.color.chart_baseline)
            alpha = 70
            strokeWidth = dp(0.7f, density)
        }
        for (frac in listOf(0.33f, 0.66f)) {
            val y = plotB - plotH * frac
            canvas.drawLine(plotL, y, plotR, y, gridPaint)
        }

        // Baseline hairline along the bottom of the plot. chart_baseline is a
        // dynamic system color with no baked alpha, so the hairline weight is
        // applied here.
        val baselinePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = ContextCompat.getColor(context, R.color.chart_baseline)
            alpha = 36
            strokeWidth = dp(1f, density)
        }
        canvas.drawLine(plotL, plotB, plotR, plotB, baselinePaint)

        val alarmColor = ContextCompat.getColor(context, R.color.chart_buienalarm)
        val radarColor = ContextCompat.getColor(context, R.color.chart_buienradar)

        // Filled area under each series at low alpha gives intensity the
        // visual weight a flat polyline can't: a drizzle leaves a sliver,
        // heavy rain visibly fills the chart. Draw radar first so alarm's
        // fill overlaps on top (matching the line stacking order).
        seriesFill(radar, ::xOf, ::yOf, plotB)?.let {
            canvas.drawPath(it, fillPaint(radarColor))
        }
        seriesFill(alarm, ::xOf, ::yOf, plotB)?.let {
            canvas.drawPath(it, fillPaint(alarmColor))
        }
        seriesPath(alarm, ::xOf, ::yOf)?.let {
            canvas.drawPath(it, strokePaintColor(alarmColor, density))
        }
        seriesPath(radar, ::xOf, ::yOf)?.let {
            canvas.drawPath(it, strokePaintColor(radarColor, density))
        }

        val tickPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = ContextCompat.getColor(context, R.color.widget_subtle)
            textSize = sp(11f, context)
            isAntiAlias = true
        }

        // Baseline of the single time row below the plot.
        val yTimeRow = plotB + dp(13f, density)

        val zone = ZoneId.systemDefault()

        // Sun markers (vertical hairlines + glyph) — drawn before the
        // axis labels so the labels overlay them cleanly at the edges.
        val sunPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = ContextCompat.getColor(context, R.color.chart_temp)
            alpha = 110
            strokeWidth = dp(1f, density)
            style = Paint.Style.STROKE
        }
        val sunGlyphPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = ContextCompat.getColor(context, R.color.chart_temp)
            textSize = sp(13f, context)
            isAntiAlias = true
        }
        for (ev in data.sunEvents) {
            if (ev.time.isBefore(tMin) || ev.time.isAfter(tMax)) continue
            val x = xOf(ev.time)
            canvas.drawLine(x, plotT, x, plotB, sunPaint)
            val glyph = if (ev.kind == "sunrise") "↑" else "↓"
            val label = "$glyph " + ZonedDateTime.ofInstant(ev.time, zone).format(hhmm)
            drawCenteredText(canvas, label, x, plotT - dp(2f, density), sunGlyphPaint)
        }

        // Inner ticks anchored to now (+30 / +60 / +90), so spacing is
        // always consistent and the last interior tick can never collide
        // with the right-edge "end" label. Each label still reads as an
        // absolute HH:mm timestamp.
        val interiorStepMin = 30L
        var offsetMin = interiorStepMin
        while (true) {
            val t = nowInstant.plusSeconds(offsetMin * 60)
            // Stop ~15 min before end so the last tick has visual breathing room.
            if ((tMax.toEpochMilli() - t.toEpochMilli()) / 60_000 < 15) break
            val x = xOf(t)
            drawCenteredText(canvas, ZonedDateTime.ofInstant(t, zone).format(hhmm), x, yTimeRow, tickPaint)
            offsetMin += interiorStepMin
        }

        // Edge time labels anchored to the chart corners.
        val xNow = xOf(nowInstant)
        val xEnd = xOf(tMax)
        val nowTime = ZonedDateTime.ofInstant(nowInstant, zone).format(hhmm)
        val endTime = ZonedDateTime.ofInstant(tMax, zone).format(hhmm)

        drawCenteredText(canvas, nowTime, xNow, yTimeRow, tickPaint, anchor = TextAnchor.LEFT)
        drawCenteredText(canvas, endTime, xEnd, yTimeRow, tickPaint, anchor = TextAnchor.RIGHT)

        // Right-edge intensity reference: up to two faint numeric labels
        // at "nice" mm/h levels that fall within the visible y range.
        val refPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = ContextCompat.getColor(context, R.color.widget_subtle)
            textSize = sp(9f, context)
            isAntiAlias = true
        }
        val niceLevels = listOf(0.2, 0.5, 1.0, 2.0, 5.0, 10.0)
            .filter { it < yHi * 0.95 && it >= yHi * 0.15 }
            .takeLast(2)
        for (v in niceLevels) {
            val y = yOf(v)
            val label = formatMm(v)
            val lw = refPaint.measureText(label)
            canvas.drawText(label, plotR - lw, y - dp(2f, density), refPaint)
        }
    }

    private fun formatMm(v: Double): String =
        if (v < 1.0) "%.1f".format(v) else "%.0f".format(v)

    // ─────────────── dry hero state (Material 3) ───────────────
    // Mirrors Google's Material 3 weather-widget style: a big current
    // temperature with the headline on the left, and a rounded tonal
    // "surfaceContainer" panel on the right listing the secondary stats
    // (feels / wind / UV) as label → now · +2h rows. NOW is the hero; +2H is
    // smaller and warm so the eye lands on the present value first. No provider
    // strip (flat dry data reads as a fake progress bar) and no provider labels.
    private fun drawHero(
        context: Context,
        canvas: Canvas,
        w: Int,
        h: Int,
        data: Data,
    ) {
        val density = context.resources.displayMetrics.density
        val fg = ContextCompat.getColor(context, R.color.widget_text)
        val muted = ContextCompat.getColor(context, R.color.widget_subtle)
        val warm = ContextCompat.getColor(context, R.color.chart_temp)
        val container = ContextCompat.getColor(context, R.color.widget_container)
        val scale = (h / dp(150f, density)).coerceIn(0.82f, 1.08f)

        val sidePad = dp(16f, density)

        // Right-hand tonal stats panel — the Material "surfaceContainer".
        val panelW = ((w - sidePad * 2f) * 0.42f).coerceAtLeast(dp(132f, density))
        val panelLeft = w - sidePad - panelW
        val panelTop = dp(8f, density)
        val panelBottom = h - dp(8f, density)
        val panelRadius = dp(20f, density)
        canvas.drawRoundRect(
            RectF(panelLeft, panelTop, w - sidePad, panelBottom),
            panelRadius, panelRadius,
            Paint(Paint.ANTI_ALIAS_FLAG).apply { color = container; style = Paint.Style.FILL },
        )

        val leftW = panelLeft - dp(14f, density) - sidePad

        val headlinePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = fg
            textSize = sp(15f * scale, context)
            isAntiAlias = true
            typeface = weight(500)
        }
        val labelPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = muted
            textSize = sp(10.5f * scale, context)
            isAntiAlias = true
            typeface = weight(700)
            letterSpacing = 0.16f
        }
        // Light, oversized numerals read as modern and calm where chunky bold
        // reads as a cheap default.
        val nowTempPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = fg
            textSize = sp(52f * scale, context)
            isAntiAlias = true
            typeface = weight(350)
        }
        val endTempPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = warm
            textSize = sp(18f * scale, context)
            isAntiAlias = true
            typeface = weight(500)
        }
        val rowLabelPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = muted
            textSize = sp(11f * scale, context)
            isAntiAlias = true
            typeface = weight(600)
            letterSpacing = 0.06f
        }
        val rowValPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            textSize = sp(15f * scale, context)
            isAntiAlias = true
            typeface = weight(600)
        }

        // ── Left column: headline (top), big NOW temp + small warm +2H. ──
        val headlineBaseline = dp(11f, density) - headlinePaint.fontMetrics.ascent
        val headline = data.dryHeadline?.takeIf { it.isNotBlank() }
            ?: "Dry for the next 2 hours"
        fitText(headlinePaint, headline, leftW, sp(12f, context))
        canvas.drawText(headline, sidePad, headlineBaseline, headlinePaint)

        // Center the temperature block in the space below the headline.
        fun lineH(p: Paint) = p.fontMetrics.let { it.descent - it.ascent }
        val contentTop = headlineBaseline + headlinePaint.fontMetrics.descent + dp(6f, density)
        val contentBottom = h - dp(12f, density)
        val gapTempToEnd = dp(3f, density)
        val blockH = lineH(nowTempPaint) + gapTempToEnd + lineH(endTempPaint)
        val blockTop = (contentTop + ((contentBottom - contentTop) - blockH) / 2f)
            .coerceAtLeast(contentTop)
        val tempBaseline = blockTop - nowTempPaint.fontMetrics.ascent
        canvas.drawText("${data.tempNow}°", sidePad, tempBaseline, nowTempPaint)

        // "+2H 21°" — label in muted small caps, value warm.
        val endBaseline = tempBaseline + nowTempPaint.fontMetrics.descent +
                gapTempToEnd - endTempPaint.fontMetrics.ascent
        canvas.drawText("+2H", sidePad, endBaseline, labelPaint)
        val endLabelW = labelPaint.measureText("+2H") + dp(7f, density)
        canvas.drawText("${data.tempEnd}°", sidePad + endLabelW, endBaseline, endTempPaint)

        // ── Right panel: FEELS / WIND / UV, each as now (fg) · +2h (muted). ──
        val innerL = panelLeft + dp(14f, density)
        val innerR = w - sidePad - dp(14f, density)
        val innerTop = panelTop + dp(13f, density)
        val innerBottom = panelBottom - dp(13f, density)
        val rows = buildList {
            data.microNow?.let { n ->
                val e = data.microEnd
                add(Triple("FEELS", "${n.feels}°", e?.let { "${it.feels}°" }))
                add(Triple("WIND", "${n.windArrow}${n.windKmh}", e?.let { "${it.windArrow}${it.windKmh}" }))
                add(Triple("UV", "${n.uv}", e?.let { "${it.uv}" }))
            }
        }
        if (rows.isNotEmpty()) {
            val slot = (innerBottom - innerTop) / rows.size
            val mid = -(rowValPaint.fontMetrics.ascent + rowValPaint.fontMetrics.descent) / 2f
            for ((i, row) in rows.withIndex()) {
                val (label, nowVal, endVal) = row
                val baseline = innerTop + slot * i + slot / 2f + mid
                rowLabelPaint.color = muted
                canvas.drawText(label, innerL, baseline, rowLabelPaint)
                var x = innerR
                if (endVal != null) {
                    rowValPaint.color = muted
                    x -= rowValPaint.measureText(endVal)
                    canvas.drawText(endVal, x, baseline, rowValPaint)
                    x -= dp(8f, density)
                }
                rowValPaint.color = fg
                canvas.drawText(nowVal, x - rowValPaint.measureText(nowVal), baseline, rowValPaint)
            }
        }
    }

    private fun weight(w: Int): android.graphics.Typeface =
        android.graphics.Typeface.create(android.graphics.Typeface.DEFAULT, w, false)

    // ─────────────── helpers ───────────────
    private fun strokePaintColor(colorInt: Int, density: Float): Paint =
        Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = colorInt
            strokeWidth = dp(1.7f, density)
            style = Paint.Style.STROKE
            strokeCap = Paint.Cap.ROUND
            strokeJoin = Paint.Join.ROUND
        }

    private fun fillPaint(colorInt: Int): Paint =
        Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = colorInt
            alpha = 38 // visible weight without overpowering the line
            style = Paint.Style.FILL
        }

    private fun seriesFill(
        series: List<Pair<Instant, Double>>,
        xOf: (Instant) -> Float,
        yOf: (Double) -> Float,
        plotB: Float,
    ): Path? {
        if (series.size < 2) return null
        val p = Path()
        val first = series.first()
        p.moveTo(xOf(first.first), plotB)
        for ((t, v) in series) {
            p.lineTo(xOf(t), yOf(v.coerceAtLeast(0.0)))
        }
        val last = series.last()
        p.lineTo(xOf(last.first), plotB)
        p.close()
        return p
    }

    private fun seriesPath(
        series: List<Pair<Instant, Double>>,
        xOf: (Instant) -> Float,
        yOf: (Double) -> Float,
    ): Path? {
        if (series.size < 2) return null
        val p = Path()
        var started = false
        for ((t, v) in series) {
            val x = xOf(t)
            val y = yOf(v.coerceAtLeast(0.0))
            if (!started) { p.moveTo(x, y); started = true } else { p.lineTo(x, y) }
        }
        return p
    }

    private fun parse(points: List<PointDto>): List<Pair<Instant, Double>> =
        points.mapNotNull { pt ->
            runCatching { OffsetDateTime.parse(pt.time).toInstant() to pt.value }.getOrNull()
        }.sortedBy { it.first }

    private enum class TextAnchor { LEFT, CENTER, RIGHT }

    private fun drawCenteredText(
        canvas: Canvas,
        text: String,
        x: Float,
        y: Float,
        paint: Paint,
        anchor: TextAnchor = TextAnchor.CENTER,
    ) {
        val tw = paint.measureText(text)
        val dx = when (anchor) {
            TextAnchor.LEFT -> 0f
            TextAnchor.CENTER -> -tw / 2f
            TextAnchor.RIGHT -> -tw
        }
        canvas.drawText(text, x + dx, y, paint)
    }

    private fun fitText(paint: Paint, text: String, maxWidth: Float, minTextSize: Float) {
        while (paint.measureText(text) > maxWidth && paint.textSize > minTextSize) {
            paint.textSize *= 0.94f
        }
    }

    private fun dp(v: Float, density: Float): Float = v * density
    private fun sp(v: Float, context: Context): Float =
        TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_SP, v, context.resources.displayMetrics)

    /** For the worker's error / no-location fallback: a fresh bitmap with a
     *  single centered line. */
    fun renderMessage(context: Context, widthPx: Int, heightPx: Int, message: String): Bitmap {
        val bmp = Bitmap.createBitmap(widthPx, heightPx, Bitmap.Config.ARGB_8888)
        drawMessage(context, Canvas(bmp), widthPx, heightPx, message)
        return bmp
    }

    /** Centers `message` on an existing canvas. Shared by [renderMessage] and
     *  the in-[render] "no nowcast" branch. */
    private fun drawMessage(context: Context, canvas: Canvas, widthPx: Int, heightPx: Int, message: String) {
        val paint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = ContextCompat.getColor(context, R.color.widget_subtle)
            textSize = sp(12f, context)
        }
        val bounds = Rect()
        paint.getTextBounds(message, 0, message.length, bounds)
        canvas.drawText(message, (widthPx - bounds.width()) / 2f, (heightPx + bounds.height()) / 2f, paint)
    }
}
