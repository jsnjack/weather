package net.surfly.weather.widget.render

import android.content.Context
import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Paint
import android.graphics.Path
import android.graphics.Rect
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
 * Renders the v4 widget chart: rainy state (two thin polylines with
 * framing temperature numbers), or a quiet hero for the dry state.
 * Mirrors the visual language of `/tmp/mockups/v4_*.png`.
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

    // Caution thresholds — pick colours per metric.
    private const val WIND_CAUTION_KMH = 28   // Beaufort 5+ (fresh breeze)
    private const val WIND_CRITICAL_KMH = 50  // Beaufort 7+ (near gale)
    private const val UV_CAUTION = 3          // sunscreen recommended
    private const val UV_CRITICAL = 8         // very-high / extreme

    /** True when both providers stay under the dry threshold across the
     *  whole window — i.e. the hero (not the chart) will be rendered.
     *  Exposed so callers can swap surrounding UI accordingly (e.g.
     *  blank the peak TextView so it doesn't duplicate the hero text). */
    fun isDryWindow(alarm: List<PointDto>, radar: List<PointDto>): Boolean {
        val peak = maxOf(
            alarm.maxOfOrNull { it.value } ?: 0.0,
            radar.maxOfOrNull { it.value } ?: 0.0,
        )
        return peak < DRY_THRESHOLD
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

        val alarm = parse(data.buienalarm)
        val radarRaw = parse(data.buienradar)
        // No points from either provider is a distinct state from "dry": show
        // an explicit message rather than the dry hero, which would imply we
        // know it's dry when we actually have no nowcast at all.
        if (alarm.isEmpty() && radarRaw.isEmpty()) {
            drawMessage(context, canvas, widthPx, heightPx, context.getString(R.string.state_no_data))
            return bmp
        }
        // Cap radar to Buienalarm's horizon so both lines share the same x range.
        // Buienalarm is the shorter (and authoritative) nowcast window.
        val alarmLast = alarm.lastOrNull()?.first
        val radar = if (alarmLast != null) radarRaw.filter { !it.first.isAfter(alarmLast) } else radarRaw

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
        val padTop = dp(6f, density)
        // Bottom area carries two stacked rows per corner:
        // row 1: time HH:mm   row 2: big temp + inline micro stats.
        val padBottom = dp(36f, density)

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

        // Baseline hairline along the bottom of the plot.
        val baselinePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = ContextCompat.getColor(context, R.color.chart_baseline)
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
        val tempPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = ContextCompat.getColor(context, R.color.chart_temp)
            textSize = sp(20f, context)
            isAntiAlias = true
            typeface = android.graphics.Typeface.DEFAULT_BOLD
        }
        val microSize = sp(13f, context)
        val microMuted = ContextCompat.getColor(context, R.color.widget_subtle)
        val microCaution = ContextCompat.getColor(context, R.color.chart_caution)
        val microCritical = ContextCompat.getColor(context, R.color.chart_critical)
        val microPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = microMuted
            textSize = microSize
            isAntiAlias = true
        }

        // Vertical y-positions for two rows below the plot. The combined
        // row uses the temp baseline; micros sit on the same baseline so
        // their visual top aligns with the temp number's mid-height.
        val yTimeRow = plotB + dp(13f, density)
        val yCombined = plotB + dp(31f, density)

        val zone = ZoneId.systemDefault()

        // Sun markers (vertical hairlines + glyph) — drawn before the
        // axis labels so the labels overlay them cleanly at the edges.
        val sunPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = ContextCompat.getColor(context, R.color.chart_temp)
            alpha = 110
            strokeWidth = dp(1f, density)
            style = Paint.Style.STROKE
        }
        val sunGlyphPaint = Paint(microPaint).apply {
            color = ContextCompat.getColor(context, R.color.chart_temp)
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

        // Per-corner stacks. Each stack is anchored to the chart edge.
        val xNow = xOf(nowInstant)
        val xEnd = xOf(tMax)
        val nowTime = ZonedDateTime.ofInstant(nowInstant, zone).format(hhmm)
        val endTime = ZonedDateTime.ofInstant(tMax, zone).format(hhmm)

        drawCenteredText(canvas, nowTime, xNow, yTimeRow, tickPaint, anchor = TextAnchor.LEFT)
        drawCenteredText(canvas, endTime, xEnd, yTimeRow, tickPaint, anchor = TextAnchor.RIGHT)

        // Left corner: big temp anchored LEFT at xNow, micros flow right.
        val tempNowText = "${data.tempNow}°"
        val tempNowWidth = tempPaint.measureText(tempNowText)
        canvas.drawText(tempNowText, xNow, yCombined, tempPaint)
        data.microNow?.let { m ->
            val segments = microSegments(m, microMuted, microCaution, microCritical)
            drawSegments(canvas, segments, xNow + tempNowWidth + dp(6f, density), yCombined, microPaint)
        }

        // Right corner: big temp anchored RIGHT at xEnd, micros sit just
        // to its left, right-edge-aligned so they hug the temp.
        val tempEndText = "${data.tempEnd}°"
        val tempEndWidth = tempPaint.measureText(tempEndText)
        canvas.drawText(tempEndText, xEnd - tempEndWidth, yCombined, tempPaint)
        data.microEnd?.let { m ->
            val segments = microSegments(m, microMuted, microCaution, microCritical)
            val totalWidth = segments.sumOf { microPaint.measureText(it.first).toDouble() }.toFloat()
            val startX = xEnd - tempEndWidth - dp(6f, density) - totalWidth
            drawSegments(canvas, segments, startX, yCombined, microPaint)
        }

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

    /** Break a Micro into (text, colour) segments so wind and UV can be
     *  highlighted when they cross caution/critical thresholds. */
    private fun microSegments(
        m: Micro,
        muted: Int,
        caution: Int,
        critical: Int,
    ): List<Pair<String, Int>> {
        val windColour = when {
            m.windKmh >= WIND_CRITICAL_KMH -> critical
            m.windKmh >= WIND_CAUTION_KMH -> caution
            else -> muted
        }
        val uvColour = when {
            m.uv >= UV_CRITICAL -> critical
            m.uv >= UV_CAUTION -> caution
            else -> muted
        }
        return listOf(
            "≈${m.feels}°"                to muted,
            " · "                          to muted,
            "${m.windArrow}${m.windKmh}"   to windColour,
            " · "                          to muted,
            "UV ${m.uv}"                   to uvColour,
        )
    }

    /** Draw (text, colour) segments left-to-right starting at startX. */
    private fun drawSegments(
        canvas: Canvas,
        segments: List<Pair<String, Int>>,
        startX: Float,
        baseline: Float,
        paint: Paint,
    ) {
        var x = startX
        for ((text, color) in segments) {
            paint.color = color
            canvas.drawText(text, x, baseline, paint)
            x += paint.measureText(text)
        }
    }


    // ─────────────── dry hero state ───────────────
    // V2 layout: bold headline up top (Buienalarm desc when present, else
    // a generic "Dry for the next 2 hours"), then two side-by-side
    // columns (NOW / +2H) each with a big temperature and a
    // feels·wind·UV sub-line. The whole stack is vertically centered so
    // tall widgets don't leave dead space at the bottom.
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
        val caution = ContextCompat.getColor(context, R.color.chart_caution)
        val critical = ContextCompat.getColor(context, R.color.chart_critical)
        val divider = ContextCompat.getColor(context, R.color.chart_baseline)

        val headlinePaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = fg
            textSize = sp(26f, context)
            isAntiAlias = true
            typeface = android.graphics.Typeface.DEFAULT
        }
        val labelPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = muted
            textSize = sp(13f, context)
            isAntiAlias = true
            typeface = android.graphics.Typeface.DEFAULT_BOLD
            letterSpacing = 0.12f
        }
        val tempPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = fg
            textSize = sp(60f, context)
            isAntiAlias = true
            typeface = android.graphics.Typeface.DEFAULT_BOLD
        }
        val subPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = muted
            textSize = sp(17f, context)
            isAntiAlias = true
        }

        val gapHeadlineToLabel = dp(18f, density)
        val gapLabelToTemp = dp(8f, density)
        val gapTempToSub = dp(10f, density)

        // Compute each row's vertical extent from its Paint metrics so the
        // stack is honestly centered against the canvas height.
        fun lineH(p: Paint) = p.fontMetrics.let { it.descent - it.ascent }
        val totalH = lineH(headlinePaint) + gapHeadlineToLabel +
                lineH(labelPaint) + gapLabelToTemp +
                lineH(tempPaint) + gapTempToSub +
                lineH(subPaint)
        val topY = (h - totalH) / 2f

        // Each *Baseline is the y coordinate to pass to canvas.drawText.
        val headlineBaseline = topY - headlinePaint.fontMetrics.ascent
        val labelBaseline = headlineBaseline + headlinePaint.fontMetrics.descent +
                gapHeadlineToLabel - labelPaint.fontMetrics.ascent
        val tempBaseline = labelBaseline + labelPaint.fontMetrics.descent +
                gapLabelToTemp - tempPaint.fontMetrics.ascent
        val subBaseline = tempBaseline + tempPaint.fontMetrics.descent +
                gapTempToSub - subPaint.fontMetrics.ascent

        val headline = data.dryHeadline?.takeIf { it.isNotBlank() }
            ?: "Dry for the next 2 hours"
        val hlw = headlinePaint.measureText(headline)
        canvas.drawText(headline, (w - hlw) / 2f, headlineBaseline, headlinePaint)

        // Divider runs between NOW and +2H columns, vertically aligned
        // with the temperature row.
        val dividerPaint = Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = divider
            strokeWidth = dp(1f, density)
        }
        canvas.drawLine(
            w / 2f, labelBaseline + labelPaint.fontMetrics.descent,
            w / 2f, subBaseline - subPaint.fontMetrics.ascent,
            dividerPaint,
        )

        drawColumn(
            canvas, w / 4f,
            label = "NOW", labelPaint = labelPaint, labelBaseline = labelBaseline,
            temp = data.tempNow, tempColor = fg, tempPaint = tempPaint, tempBaseline = tempBaseline,
            micro = data.microNow, subPaint = subPaint, muted = muted,
            caution = caution, critical = critical, subBaseline = subBaseline,
        )
        drawColumn(
            canvas, w * 3f / 4f,
            label = "+2H", labelPaint = labelPaint, labelBaseline = labelBaseline,
            temp = data.tempEnd, tempColor = warm, tempPaint = tempPaint, tempBaseline = tempBaseline,
            micro = data.microEnd, subPaint = subPaint, muted = muted,
            caution = caution, critical = critical, subBaseline = subBaseline,
        )
    }

    private fun drawColumn(
        canvas: Canvas,
        cx: Float,
        label: String,
        labelPaint: Paint,
        labelBaseline: Float,
        temp: Int,
        tempColor: Int,
        tempPaint: Paint,
        tempBaseline: Float,
        micro: Micro?,
        subPaint: Paint,
        muted: Int,
        caution: Int,
        critical: Int,
        subBaseline: Float,
    ) {
        // Label (NOW / +2H), small caps, muted.
        val lw = labelPaint.measureText(label)
        canvas.drawText(label, cx - lw / 2f, labelBaseline, labelPaint)

        // Big temperature.
        val tempText = "${temp}°"
        tempPaint.color = tempColor
        val tw = tempPaint.measureText(tempText)
        canvas.drawText(tempText, cx - tw / 2f, tempBaseline, tempPaint)

        // Sub-line: feels · wind · UV with caution/critical colouring per
        // metric. Compact format (matches the rainy chart's corner micros)
        // so a larger font still fits in the column's half-width.
        if (micro != null) {
            val sep = "   "
            val feels = "≈${micro.feels}°"
            val wind = "${micro.windArrow}${micro.windKmh}"
            val uv = "UV ${micro.uv}"
            val windColor = when {
                micro.windKmh >= WIND_CRITICAL_KMH -> critical
                micro.windKmh >= WIND_CAUTION_KMH -> caution
                else -> muted
            }
            val uvColor = when {
                micro.uv >= UV_CRITICAL -> critical
                micro.uv >= UV_CAUTION -> caution
                else -> muted
            }
            val segments = listOf(
                feels to muted,
                sep to muted,
                wind to windColor,
                sep to muted,
                uv to uvColor,
            )
            val totalW = segments.sumOf { subPaint.measureText(it.first).toDouble() }.toFloat()
            var x = cx - totalW / 2f
            for ((text, color) in segments) {
                subPaint.color = color
                canvas.drawText(text, x, subBaseline, subPaint)
                x += subPaint.measureText(text)
            }
        }
    }

    // ─────────────── helpers ───────────────
    private fun strokePaintColor(colorInt: Int, density: Float): Paint =
        Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = colorInt
            strokeWidth = dp(2f, density)
            style = Paint.Style.STROKE
            strokeCap = Paint.Cap.ROUND
            strokeJoin = Paint.Join.ROUND
        }

    private fun fillPaint(colorInt: Int): Paint =
        Paint(Paint.ANTI_ALIAS_FLAG).apply {
            color = colorInt
            alpha = 56 // ~22% opacity — visible weight without overpowering the line
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
