package net.surfly.weather.widget

/** When an automatic refresh is worth doing. Pure, so JVM tests cover it. */
object RefreshPolicy {
    /** A forecast this old is refreshed when the screen-on alarm arrives. */
    const val STALE_AFTER_MS = 4 * 60_000L

    /** Clock skew tolerated before a save time in the future counts as wrong. */
    private const val FUTURE_SLACK_MS = 60_000L

    /** True when there is no saved forecast, it is old, or its time is implausible. */
    fun isStale(savedAtMs: Long?, nowMs: Long): Boolean =
        savedAtMs == null || nowMs - savedAtMs >= STALE_AFTER_MS || savedAtMs - nowMs > FUTURE_SLACK_MS
}
