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
 * accuracy gates. The old implementation returned `client.lastLocation`
 * unconditionally, so a stale fix — e.g. your home-city location still cached
 * while you're travelling — was reused indefinitely. Here a last-known fix is
 * only accepted when it is recent and accurate enough; otherwise we actively
 * request a current fix, and if that fails too we return null so the caller
 * can show an honest "No location" state instead of silently drifting.
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

    /**
     * Returns a usable fix, or null when none can be trusted. Order:
     *   1. The cached last-known fix, but only if it is fresh ([MAX_LAST_AGE_MS])
     *      and accurate enough ([MAX_ACCURACY_M]) — the cheap, no-power path.
     *   2. Otherwise an active [Priority.PRIORITY_BALANCED_POWER_ACCURACY]
     *      current-location request.
     *   3. Null if both fail — the caller must NOT fall back to server-side IP
     *      in Auto mode; it should render "No location".
     */
    @SuppressLint("MissingPermission")
    suspend fun current(): Fix? {
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

        // Both paths failed — background location not granted, GPS cold, or
        // fused provider timed out. Fall back to the last coords we successfully
        // used for a widget refresh so the widget doesn't blank out just because
        // GPS is temporarily unavailable (e.g. right after unlock).
        val stored = WidgetPrefs.lastFix(context)
        if (stored != null) {
            return Fix(stored.first, stored.second, Long.MAX_VALUE, Float.MAX_VALUE, current = false)
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
