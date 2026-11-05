package net.surfly.weather.widget.render

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class WidgetPlannerTest {
    @Test
    fun fourByTwoMatchesTheMockups() {
        val plan = WidgetPlanner.plan(384f, 220f)
        assertEquals(Variant.FULL, plan.variant)
        assertEquals(166f, plan.mapDp, 0.01f)
        assertEquals(186f, plan.laneDp, 0.01f)
        assertEquals(72f, plan.chartHeightDp, 0.01f)
        assertTrue(plan.showTable)
    }

    @Test
    fun mapStaysSquareAndInsideTheBody() {
        for (w in listOf(330f, 384f, 412f, 500f)) {
            val plan = WidgetPlanner.plan(w, 220f)
            assertEquals(Variant.FULL, plan.variant)
            val body = 220f - 2 * WidgetPlanner.PADDING - WidgetPlanner.HEADER - WidgetPlanner.GAP
            assertTrue("map taller than body at $w", plan.mapDp <= body)
            assertEquals("columns don't fill $w", w - 2 * WidgetPlanner.PADDING, plan.laneDp + WidgetPlanner.GAP + plan.mapDp, 0.01f)
        }
    }

    @Test
    fun narrowWidgetsDropTheMapBySizeOnly() {
        val plan = WidgetPlanner.plan(288f, 220f)
        assertEquals(Variant.PANEL, plan.variant)
        assertEquals(0f, plan.mapDp, 0f)
        assertEquals(264f, plan.chartWidthDp, 0.01f)
        assertTrue(plan.showTable)
        assertFalse(WidgetPlanner.plan(184f, 220f).showTable)
    }

    @Test
    fun shortWidgetsUseOneRow() {
        val plan = WidgetPlanner.plan(384f, 110f)
        assertEquals(Variant.ROW, plan.variant)
        assertEquals(86f, plan.mapDp, 0.01f)
        assertEquals(166f, plan.laneDp, 0.01f)
        assertEquals(0f, WidgetPlanner.plan(250f, 110f).mapDp, 0f)
    }

    @Test
    fun largerFontsGetTallerRowsOrAShorterLayout() {
        val normal = WidgetPlanner.plan(384f, 220f, 1f)
        val large = WidgetPlanner.plan(384f, 240f, 1.15f)
        assertEquals(Variant.FULL, large.variant)
        assertTrue(large.condDp > normal.condDp)
        assertEquals(Variant.ROW, WidgetPlanner.plan(384f, 215f, 1.3f).variant)
    }
}
