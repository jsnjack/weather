package net.surfly.weather.widget

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RefreshPolicyTest {
    private val now = 1_795_116_600_000L
    private val minute = 60_000L

    @Test
    fun refreshesWhenNothingIsSaved() {
        assertTrue(RefreshPolicy.isStale(null, now))
    }

    @Test
    fun keepsAForecastSavedMinutesAgo() {
        assertFalse(RefreshPolicy.isStale(now - 3 * minute, now))
    }

    @Test
    fun refreshesFromFourMinutes() {
        assertTrue(RefreshPolicy.isStale(now - 4 * minute, now))
        assertTrue(RefreshPolicy.isStale(now - 90 * minute, now))
    }

    @Test
    fun refreshesWhenTheSaveTimeIsInTheFuture() {
        assertFalse(RefreshPolicy.isStale(now + 30_000L, now))
        assertTrue(RefreshPolicy.isStale(now + 10 * minute, now))
    }
}
