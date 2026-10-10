package net.surfly.weather.widget.net

import java.time.Duration
import java.time.Instant

internal object RadarPolicy {
    fun isFresh(observedAt: Instant, now: Instant): Boolean {
        val age = Duration.between(observedAt, now)
        return age >= Duration.ofMinutes(-2) && age <= Duration.ofMinutes(20)
    }

    fun fits(widthDp: Int, heightDp: Int, fontScale: Float): Boolean =
        widthDp >= 340 && heightDp >= (210 * fontScale.coerceAtLeast(1f)).toInt()
}
