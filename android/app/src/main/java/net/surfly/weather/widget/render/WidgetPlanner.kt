package net.surfly.weather.widget.render

/**
 * Layout chosen from the widget's size alone — never from the weather, so a
 * refresh can't swap layouts. Mirrors widget_stacked.xml (FULL and PANEL)
 * and widget_stacked_row.xml (ROW).
 */
enum class Variant {
    /** Conditions panel and rain lane on the left, square map on the right. */
    FULL,

    /** Conditions panel and rain lane at full width; too narrow for a map. */
    PANEL,

    /** One row: conditions, rain lane and (when wide enough) a square map. */
    ROW,
}

/** dp measurements for one widget size. */
data class WidgetPlan(
    val variant: Variant,
    /** Square map edge, or 0 when the variant has no map. */
    val mapDp: Float,
    /** Width of the rain lane, which is also the chart width. */
    val laneDp: Float,
    val chartWidthDp: Float,
    val chartHeightDp: Float,
    /** Conditions panel height (FULL and PANEL), scaled with the font size. */
    val condDp: Float,
    /** NOW / +2H table beside the temperature (FULL and PANEL only). */
    val showTable: Boolean,
)

object WidgetPlanner {
    const val PADDING = 12f
    const val GAP = 8f
    const val HEADER = 22f
    const val COND = 66f
    const val LANE_TITLE = 20f
    const val ROW_COND = 92f

    /** Narrowest left column that still fits the temperature and table. */
    private const val MIN_LEFT = 186f

    /** A map smaller than this is too small to read, so PANEL drops it. */
    private const val MIN_MAP = 110f

    /** Below this height the stacked column can't fit a useful chart. */
    private const val MIN_TALL = 190f

    /** ROW widgets narrower than this have no room for a map. */
    private const val MIN_ROW_MAP_WIDTH = 300f

    /** PANEL widgets narrower than this hide the table. */
    private const val MIN_TABLE_WIDTH = 180f

    fun plan(widthDp: Float, heightDp: Float, fontScale: Float = 1f): WidgetPlan {
        val scale = fontScale.coerceAtLeast(1f)
        val innerW = (widthDp - 2 * PADDING).coerceAtLeast(0f)
        val innerH = (heightDp - 2 * PADDING).coerceAtLeast(0f)
        val title = LANE_TITLE * scale
        if (heightDp >= MIN_TALL * scale) {
            val cond = COND * scale
            val body = innerH - HEADER * scale - GAP
            val chartH = (body - cond - GAP - title).coerceAtLeast(0f)
            val map = minOf(body, innerW - GAP - MIN_LEFT)
            if (map >= MIN_MAP) {
                val left = innerW - GAP - map
                return WidgetPlan(Variant.FULL, map, left, left, chartH, cond, showTable = true)
            }
            return WidgetPlan(
                Variant.PANEL, 0f, innerW, innerW, chartH, cond,
                showTable = innerW >= MIN_TABLE_WIDTH,
            )
        }
        val map = if (widthDp >= MIN_ROW_MAP_WIDTH) innerH else 0f
        val lane = (innerW - ROW_COND - GAP - if (map > 0f) map + GAP else 0f).coerceAtLeast(0f)
        return WidgetPlan(
            Variant.ROW, map, lane, lane, (innerH - title).coerceAtLeast(0f), 0f, showTable = false,
        )
    }
}
