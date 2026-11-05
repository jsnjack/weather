package net.surfly.weather.widget.net

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Duration
import java.time.Instant

class RadarPolicyTest {
    private val now = Instant.parse("2026-11-04T14:05:00Z")

    private fun headers(vararg pairs: Pair<String, String>): (String) -> String? = pairs.toMap()::get

    @Test
    fun freshness() {
        assertTrue(RadarPolicy.isFresh(now.minus(Duration.ofMinutes(5)), now))
        assertFalse(RadarPolicy.isFresh(now.minus(Duration.ofMinutes(21)), now))
        assertFalse(RadarPolicy.isFresh(now.plus(Duration.ofMinutes(3)), now))
        assertTrue(RadarPolicy.isFresh(now.plus(Duration.ofMinutes(1)), now))
    }

    @Test
    fun freshMapWithOutlineAndRain() {
        val verdict = RadarPolicy.classify(
            200,
            headers("X-Radar-Observed-At" to "2026-11-04T14:00:00Z", "X-Radar-Rain" to "nearby", "X-Radar-Outline" to "30"),
            now,
        )
        assertEquals(
            RadarVerdict.Show(RadarMeta(Instant.parse("2026-11-04T14:00:00Z"), unavailable = false, rainNearby = true, outline = true)),
            verdict,
        )
    }

    @Test
    fun dryMapSaysNoRain() {
        val verdict = RadarPolicy.classify(200, headers("X-Radar-Observed-At" to "2026-11-04T14:00:00Z", "X-Radar-Rain" to "none"), now)
        assertEquals(false, (verdict as RadarVerdict.Show).meta.rainNearby)
        assertFalse(verdict.meta.outline)
    }

    @Test
    fun unavailableMapIsShownWithoutObservation() {
        val verdict = RadarPolicy.classify(200, headers("X-Radar-Status" to "unavailable"), now)
        assertEquals(RadarVerdict.Show(RadarMeta(null, unavailable = true, rainNearby = null, outline = false)), verdict)
    }

    @Test
    fun staleOrUndatedMapsAreNotShown() {
        assertEquals(RadarVerdict.Stale, RadarPolicy.classify(200, headers("X-Radar-Observed-At" to "2026-11-04T13:30:00Z"), now))
        assertEquals(RadarVerdict.NoMap, RadarPolicy.classify(200, headers(), now))
        assertEquals(RadarVerdict.NoMap, RadarPolicy.classify(200, headers("X-Radar-Observed-At" to "yesterday"), now))
    }

    @Test
    fun statusCodes() {
        assertEquals(RadarVerdict.NoMap, RadarPolicy.classify(204, headers(), now))
        assertEquals(RadarVerdict.NoMap, RadarPolicy.classify(404, headers(), now))
        assertEquals(RadarVerdict.Failed, RadarPolicy.classify(502, headers(), now))
        assertEquals(RadarVerdict.Failed, RadarPolicy.classify(429, headers(), now))
    }

    @Test
    fun storedMapsAreReusedOnlyNearby() {
        assertTrue(RadarPolicy.sameArea(52.37, 4.90, 52.375, 4.905))
        assertFalse(RadarPolicy.sameArea(52.37, 4.90, 52.09, 5.12))
    }
}
