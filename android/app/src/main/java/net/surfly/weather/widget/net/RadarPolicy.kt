package net.surfly.weather.widget.net

import java.time.Duration
import java.time.Instant
import java.time.format.DateTimeParseException

/** What the server said about the local radar map. */
data class RadarMeta(
    /** Observation time; null on a map marked unavailable. */
    val observedAt: Instant?,
    /** The map has no rain because KNMI's observation is stale or missing. */
    val unavailable: Boolean,
    /** Rain inside the 50 km circle; null when the server didn't say. */
    val rainNearby: Boolean?,
    /** The 30-minute forecast outline is drawn on the map. */
    val outline: Boolean,
)

sealed interface RadarVerdict {
    /** Show the image. */
    data class Show(val meta: RadarMeta) : RadarVerdict

    /** The server has no map for this place: outside coverage, or an older server on a dry day. */
    data object NoMap : RadarVerdict

    /** The map's observation is too old to show as current. */
    data object Stale : RadarVerdict

    /** Network or server error: fall back to the last stored map. */
    data object Failed : RadarVerdict
}

object RadarPolicy {
    fun isFresh(observedAt: Instant, now: Instant): Boolean {
        val age = Duration.between(observedAt, now)
        return age >= Duration.ofMinutes(-2) && age <= Duration.ofMinutes(20)
    }

    /**
     * Interprets a `/api/v1/radar/local?always=1` response. A map marked
     * unavailable carries no rain, so it's safe to show; a map with an old
     * or missing observation time is not, because its rain would look current.
     */
    fun classify(code: Int, header: (String) -> String?, now: Instant): RadarVerdict {
        if (code == 204 || code == 404) return RadarVerdict.NoMap
        if (code !in 200..299) return RadarVerdict.Failed
        val rain = when (header("X-Radar-Rain")) {
            "nearby" -> true
            "none" -> false
            else -> null
        }
        if (header("X-Radar-Status") == "unavailable") {
            return RadarVerdict.Show(RadarMeta(null, unavailable = true, rainNearby = null, outline = false))
        }
        val observed = header("X-Radar-Observed-At")?.let {
            try {
                Instant.parse(it)
            } catch (_: DateTimeParseException) {
                null
            }
        } ?: return RadarVerdict.NoMap
        if (!isFresh(observed, now)) return RadarVerdict.Stale
        return RadarVerdict.Show(
            RadarMeta(observed, unavailable = false, rainNearby = rain, outline = header("X-Radar-Outline") == "30"),
        )
    }

    /** A stored map is reused offline only near the place it was drawn for. */
    fun sameArea(lat0: Double, lon0: Double, lat1: Double, lon1: Double): Boolean =
        distanceM(lat0, lon0, lat1, lon1) <= 2_000.0

    fun distanceM(lat0: Double, lon0: Double, lat1: Double, lon1: Double): Double {
        val r = 6_371_000.0
        val dLat = Math.toRadians(lat1 - lat0)
        val dLon = Math.toRadians(lon1 - lon0)
        val a = Math.sin(dLat / 2).let { it * it } +
            Math.cos(Math.toRadians(lat0)) * Math.cos(Math.toRadians(lat1)) * Math.sin(dLon / 2).let { it * it }
        return r * 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a))
    }
}
