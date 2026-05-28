package net.surfly.weather.widget.location

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.pm.PackageManager
import android.location.Location
import android.os.SystemClock
import androidx.core.content.ContextCompat
import com.google.android.gms.location.LocationServices
import com.google.android.gms.location.Priority
import com.google.android.gms.tasks.CancellationTokenSource
import kotlinx.coroutines.tasks.await
import kotlinx.coroutines.withTimeoutOrNull
import net.surfly.weather.widget.WidgetPrefs

/**
 * Resolves the device location for the widget with explicit freshness and
 * accuracy gates. Exposes two paths so the worker can keep the UI responsive:
 *
 *   - [lastSavedFix] — the last coords successfully used for a fetch, read
 *     synchronously from prefs. Used for the first render after unlock so the
 *     chart updates within a second, instead of waiting for GPS to warm up.
 *   - [freshFix] — actively resolves a current fix (lastLocation + balanced
 *     getCurrentLocation). May take up to ~11 s and returns null when no
 *     trustworthy fix is available; the caller must NOT silently fall back to
 *     server-side IP in Auto mode.
 */
class LocationProvider(private val context: Context) {

    /** A fix we are willing to use, plus the metadata the worker needs to
     *  decide whether/how to surface its provenance. */
    data class Fix(
        val lat: Double,
        val lon: Double,
        val ageMs: Long,      // age of the underlying fix
        val accuracyM: Float, // horizontal accuracy, metres
        val current: Boolean, // true when obtained via a fresh getCurrentLocation
    )

    private val client by lazy { LocationServices.getFusedLocationProviderClient(context) }

    fun hasPermission(): Boolean = ContextCompat.checkSelfPermission(
        context, Manifest.permission.ACCESS_COARSE_LOCATION,
    ) == PackageManager.PERMISSION_GRANTED

    /** The last coords previously persisted by a successful [freshFix], or null
     *  if no fix has ever been recorded for this install. Instant — no I/O. */
    fun lastSavedFix(): Pair<Double, Double>? = WidgetPrefs.lastFix(context)

    /**
     * Actively resolves a current fix. Order:
     *   1. The cached last-known fix from the fused provider, but only if it is
     *      fresh ([MAX_LAST_AGE_MS]) and accurate ([MAX_ACCURACY_M]) — the cheap,
     *      no-power path.
     *   2. Otherwise an active [Priority.PRIORITY_BALANCED_POWER_ACCURACY]
     *      current-location request (~8 s budget).
     *
     * On success, persists the coords via [WidgetPrefs.saveLastFix] so a future
     * [lastSavedFix] call can use them.
     *
     * Returns null when both paths fail. The caller decides whether to keep
     * showing a previous render or fall back to "No location".
     */
    @SuppressLint("MissingPermission")
    suspend fun freshFix(): Fix? {
        if (!hasPermission()) return null

        val last = runCatching {
            withTimeoutOrNull(LAST_TIMEOUT_MS) { client.lastLocation.await() }
        }.getOrNull()
        if (last != null) {
            val age = ageMs(last)
            if (age <= MAX_LAST_AGE_MS && last.accuracy <= MAX_ACCURACY_M) {
                val fix = Fix(last.latitude, last.longitude, age, last.accuracy, current = false)
                WidgetPrefs.saveLastFix(context, fix.lat, fix.lon)
                return fix
            }
        }

        val cts = CancellationTokenSource()
        val fresh = runCatching {
            withTimeoutOrNull(CURRENT_TIMEOUT_MS) {
                client.getCurrentLocation(Priority.PRIORITY_BALANCED_POWER_ACCURACY, cts.token).await()
            }
        }.getOrNull()
        cts.cancel()
        if (fresh != null) {
            val fix = Fix(fresh.latitude, fresh.longitude, ageMs(fresh), fresh.accuracy, current = true)
            WidgetPrefs.saveLastFix(context, fix.lat, fix.lon)
            return fix
        }

        return null
    }

    /** Fix age from the monotonic elapsed-realtime clock, which (unlike
     *  wall-clock time) can't jump when the system clock is adjusted. */
    private fun ageMs(loc: Location): Long {
        val elapsed = (SystemClock.elapsedRealtimeNanos() - loc.elapsedRealtimeNanos) / 1_000_000L
        return if (elapsed >= 0) elapsed else 0
    }

    companion object {
        private const val LAST_TIMEOUT_MS = 3_000L
        private const val CURRENT_TIMEOUT_MS = 8_000L
        // Accept a last-known fix up to 30 min old — covers the default 15-min
        // refresh cycle with a healthy buffer. Other apps (Maps, etc.) typically
        // keep lastLocation fresher than this during normal phone use.
        private const val MAX_LAST_AGE_MS = 30 * 60_000L
        // Coarse permission gives ~city-block accuracy; anything worse than
        // ~5 km is rejected. That's still ample for a regional 2h rain nowcast.
        private const val MAX_ACCURACY_M = 5_000f
    }
}
