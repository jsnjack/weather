package net.surfly.weather.widget

import android.content.Context

enum class LocationMode { AUTO, NAME, COORDS, IP }

data class WidgetConfig(
    val serverUrl: String,
    val mode: LocationMode,
    val name: String,
    val lat: Double,
    val lon: Double,
)

object WidgetPrefs {
    private const val SHARED = "shared"
    private const val WIDGET_PREFIX = "widget_"

    private const val DEFAULT_URL = "https://weather.yauhen.cc"

    private const val K_URL = "server_url"
    private const val K_MODE = "location_mode"
    private const val K_NAME = "location_name"
    private const val K_LAT = "location_lat"
    private const val K_LON = "location_lon"
    private const val K_LAST_URL = "last_url"
    private const val K_LAST_FIX_LAT = "last_fix_lat"
    private const val K_LAST_FIX_LON = "last_fix_lon"

    fun load(context: Context, appWidgetId: Int): WidgetConfig {
        val sp = context.getSharedPreferences(WIDGET_PREFIX + appWidgetId, Context.MODE_PRIVATE)
        return WidgetConfig(
            serverUrl = sp.getString(K_URL, defaultUrl(context)) ?: DEFAULT_URL,
            mode = runCatching { LocationMode.valueOf(sp.getString(K_MODE, "AUTO") ?: "AUTO") }
                .getOrDefault(LocationMode.AUTO),
            name = sp.getString(K_NAME, "") ?: "",
            lat = sp.getFloat(K_LAT, 0f).toDouble(),
            lon = sp.getFloat(K_LON, 0f).toDouble(),
        )
    }

    fun save(context: Context, appWidgetId: Int, cfg: WidgetConfig) {
        context.getSharedPreferences(WIDGET_PREFIX + appWidgetId, Context.MODE_PRIVATE)
            .edit()
            .putString(K_URL, cfg.serverUrl)
            .putString(K_MODE, cfg.mode.name)
            .putString(K_NAME, cfg.name)
            .putFloat(K_LAT, cfg.lat.toFloat())
            .putFloat(K_LON, cfg.lon.toFloat())
            .apply()
        context.getSharedPreferences(SHARED, Context.MODE_PRIVATE)
            .edit()
            .putString(K_LAST_URL, cfg.serverUrl)
            .apply()
    }

    fun clear(context: Context, appWidgetId: Int) {
        context.getSharedPreferences(WIDGET_PREFIX + appWidgetId, Context.MODE_PRIVATE)
            .edit().clear().apply()
    }

    fun defaultUrl(context: Context): String =
        context.getSharedPreferences(SHARED, Context.MODE_PRIVATE)
            .getString(K_LAST_URL, DEFAULT_URL) ?: DEFAULT_URL

    /** Persists the last successfully-resolved lat/lon so [LocationProvider]
     *  can use it as an ultimate fallback when both lastLocation and
     *  getCurrentLocation fail (e.g. background location not granted). */
    fun saveLastFix(context: Context, lat: Double, lon: Double) {
        context.getSharedPreferences(SHARED, Context.MODE_PRIVATE)
            .edit()
            .putFloat(K_LAST_FIX_LAT, lat.toFloat())
            .putFloat(K_LAST_FIX_LON, lon.toFloat())
            .apply()
    }

    /** Returns the last saved lat/lon, or null if none has been stored yet. */
    fun lastFix(context: Context): Pair<Double, Double>? {
        val sp = context.getSharedPreferences(SHARED, Context.MODE_PRIVATE)
        val lat = sp.getFloat(K_LAST_FIX_LAT, Float.NaN)
        val lon = sp.getFloat(K_LAST_FIX_LON, Float.NaN)
        if (lat.isNaN() || lon.isNaN()) return null
        return lat.toDouble() to lon.toDouble()
    }
}
