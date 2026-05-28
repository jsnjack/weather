package net.surfly.weather.widget

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.widget.RemoteViews
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import net.surfly.weather.widget.location.LocationProvider
import net.surfly.weather.widget.net.ErrKind
import net.surfly.weather.widget.net.FetchResult
import net.surfly.weather.widget.net.GlanceApi
import net.surfly.weather.widget.net.GlanceResponse
import net.surfly.weather.widget.render.ChartRenderer
import java.io.File
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

class RainWidgetWorker(
    appContext: Context,
    params: WorkerParameters,
) : CoroutineWorker(appContext, params) {

    companion object {
        const val KEY_WIDGET_ID = "appWidgetId"
        private const val CACHE_PREFIX = "last_glance_"
        private const val MOVED_THRESHOLD_M = 500.0
        private val updatedFmt = DateTimeFormatter.ofPattern("HH:mm")
        private val json = Json { ignoreUnknownKeys = true; isLenient = true }
    }

    override suspend fun doWork(): Result = withContext(Dispatchers.IO) {
        val ctx = applicationContext
        val mgr = AppWidgetManager.getInstance(ctx)
        val targetId = inputData.getInt(KEY_WIDGET_ID, -1)
        val ids = if (targetId != -1) {
            intArrayOf(targetId)
        } else {
            mgr.getAppWidgetIds(ComponentName(ctx, RainWidgetProvider::class.java))
        }
        if (ids.isEmpty()) return@withContext Result.success()

        for (id in ids) {
            refreshOne(ctx, mgr, id)
        }
        Result.success()
    }

    private suspend fun refreshOne(ctx: Context, mgr: AppWidgetManager, id: Int) {
        val cfg = WidgetPrefs.load(ctx, id)

        // Flip the timestamp pill to "Refreshing…" right away so the user gets
        // feedback before any GPS or network work begins. In Auto mode that
        // gap was up to ~11 s of lastLocation + getCurrentLocation timeouts.
        showRefreshing(ctx, mgr, id)

        when (cfg.mode) {
            LocationMode.AUTO -> refreshAuto(ctx, mgr, id, cfg)
            LocationMode.NAME -> {
                val name = cfg.name.ifBlank { null }
                if (name == null) renderNoLocation(ctx, mgr, id, cfg)
                else fetchAndRender(ctx, mgr, id, cfg, lat = null, lon = null, nameQ = name)
            }
            LocationMode.COORDS -> fetchAndRender(ctx, mgr, id, cfg, lat = cfg.lat, lon = cfg.lon, nameQ = null)
            LocationMode.IP -> fetchAndRender(ctx, mgr, id, cfg, lat = null, lon = null, nameQ = null)
        }
    }

    /**
     * Two-pass refresh for Auto mode so the chart updates in ~1 s instead of
     * waiting up to ~11 s for GPS to warm up:
     *
     *   1. **Stored coords pass** — render with the last fix we successfully
     *      used. The user sees an updated chart almost immediately. If they're
     *      travelling this may briefly show the old city's forecast, but that
     *      is strictly better than a frozen 2-hour-old chart while GPS resolves.
     *   2. **Fresh fix pass** — kicked off in parallel; once it returns, we
     *      re-fetch only when the new coords are far enough from the stored
     *      coords to matter for a nowcast.
     *
     * Falls back to `renderNoLocation` only when no stored fix exists and the
     * fresh resolution also fails — the only true "we have no idea where you
     * are" state.
     */
    private suspend fun refreshAuto(
        ctx: Context,
        mgr: AppWidgetManager,
        id: Int,
        cfg: WidgetConfig,
    ) = coroutineScope {
        val provider = LocationProvider(ctx)
        val stored = provider.lastSavedFix()

        // Kick off the fresh fix in parallel with the cached-coords fetch.
        // Typical case on unlock: the network fetch returns in ~1 s while
        // getCurrentLocation takes the full ~8 s — so the first render appears
        // long before pass 2 even has data to evaluate.
        val freshDeferred = async { provider.freshFix() }

        if (stored != null) {
            fetchAndRender(ctx, mgr, id, cfg, lat = stored.first, lon = stored.second, nameQ = null)
        }

        val fresh = freshDeferred.await()
        if (fresh == null) {
            if (stored == null) renderNoLocation(ctx, mgr, id, cfg)
            return@coroutineScope
        }
        if (stored == null || hasMoved(stored.first, stored.second, fresh.lat, fresh.lon)) {
            // Flip the pill back to "Refreshing…" so the second-pass fetch is
            // visible — otherwise it looks like nothing is happening between
            // pass 1's "Updated HH:mm" and pass 2's "Updated HH:mm".
            showRefreshing(ctx, mgr, id)
            fetchAndRender(ctx, mgr, id, cfg, lat = fresh.lat, lon = fresh.lon, nameQ = null)
        }
    }

    private suspend fun fetchAndRender(
        ctx: Context,
        mgr: AppWidgetManager,
        id: Int,
        cfg: WidgetConfig,
        lat: Double?,
        lon: Double?,
        nameQ: String?,
    ) {
        val result = GlanceApi.fetch(cfg.serverUrl, lat, lon, nameQ)
        val response: GlanceResponse?
        val cachedAtMs: Long
        when (result) {
            is FetchResult.Ok -> {
                cacheJson(ctx, id, result.rawJson)
                response = result.response
                cachedAtMs = 0L
            }
            is FetchResult.Err -> {
                val cached = readCached(ctx, id)
                response = cached?.first
                cachedAtMs = cached?.second ?: 0L
            }
        }

        val options: Bundle = mgr.getAppWidgetOptions(id)
        val widthPx = sizePx(ctx, options, AppWidgetManager.OPTION_APPWIDGET_MAX_WIDTH, 350)
        val heightPx = sizePx(ctx, options, AppWidgetManager.OPTION_APPWIDGET_MAX_HEIGHT, 160)

        val views = RemoteViews(ctx.packageName, R.layout.widget_rain)

        if (response != null) {
            views.setImageViewResource(R.id.condition, conditionDrawable(response.condition))
            views.setTextViewText(R.id.location, response.location.description.ifBlank {
                ctx.getString(R.string.widget_label)
            })
            val alarmData = response.buienalarm?.data ?: emptyList()
            val radarData = response.buienradar?.data ?: emptyList()
            // Prefer Buienalarm's nowcast message ("Light rain starts at HH:mm",
            // "It will stay dry for now") — authoritative + human-readable.
            // The dry hero uses it as its big headline; the rainy chart shows
            // it in the small peak TextView. If the provider is silent the
            // rainy state falls back to a peak summary, and the dry state to
            // a generic "Dry for the next 2 hours".
            val alarmMessage = response.buienalarm?.desc?.takeIf { it.isNotBlank() && it != "Buienalarm" }
            val hasNowcast = ChartRenderer.hasNowcast(alarmData, radarData)
            val dryWindow = hasNowcast && ChartRenderer.isDryWindow(alarmData, radarData)
            val data = ChartRenderer.Data(
                buienalarm = alarmData,
                buienradar = radarData,
                tempNow = response.temperature.now,
                tempEnd = response.temperature.end,
                conditionLabel = conditionHuman(response.condition),
                microNow = ChartRenderer.Micro(
                    feels = response.feelsLike.now,
                    windArrow = windArrow(response.wind.now.directionDeg),
                    windKmh = response.wind.now.speedKmh,
                    uv = response.uvIndex.now,
                ),
                microEnd = ChartRenderer.Micro(
                    feels = response.feelsLike.end,
                    windArrow = windArrow(response.wind.end.directionDeg),
                    windKmh = response.wind.end.speedKmh,
                    uv = response.uvIndex.end,
                ),
                sunEvents = response.sun.mapNotNull { ev ->
                    runCatching {
                        ChartRenderer.SunEvent(ev.kind, java.time.OffsetDateTime.parse(ev.time).toInstant())
                    }.getOrNull()
                },
                dryHeadline = alarmMessage,
            )
            views.setImageViewBitmap(R.id.chart, ChartRenderer.render(ctx, widthPx, heightPx, data))
            // A failed refresh falls back to cached JSON — label it "Cached: HH:mm"
            // using the cache file's own timestamp, not the current time, so an
            // old forecast can't masquerade as a fresh one.
            val label = if (cachedAtMs > 0L) {
                ctx.getString(R.string.cached_at, formatClock(cachedAtMs))
            } else {
                ctx.getString(R.string.updated_at, formatClock(System.currentTimeMillis()))
            }
            views.setTextViewText(R.id.updated, "↻  $label")
            // Peak line: tell "no nowcast data" apart from a genuinely dry
            // window. In dry weather the hero already carries the message, so
            // the peak would just repeat it — blank it out.
            views.setTextViewText(
                R.id.peak,
                when {
                    !hasNowcast -> ctx.getString(R.string.state_no_data)
                    dryWindow -> ""
                    else -> alarmMessage ?: peakLabel(alarmData, radarData)
                },
            )
        } else {
            views.setImageViewResource(R.id.condition, R.drawable.ic_cond_clear)
            views.setImageViewBitmap(
                R.id.chart,
                ChartRenderer.renderMessage(ctx, widthPx, heightPx, errorLabel(ctx, result)),
            )
            views.setTextViewText(R.id.location, "—")
            views.setTextViewText(R.id.updated, errorLabel(ctx, result))
            views.setTextViewText(R.id.peak, "")
        }

        wireClicks(ctx, views, id, cfg.serverUrl, lat, lon, nameQ)
        mgr.updateAppWidget(id, views)
    }

    /**
     * Renders the "No location" state: shown in Auto mode when the device has no
     * fresh, accurate fix. Deliberately does NOT fetch — that would let the
     * server's IP geolocation stand in for the real location.
     *
     * If a previous forecast is cached, it is shown as-is so the widget stays
     * useful (GPS is often slow to warm up right after unlock). The timestamp
     * pill is replaced with "No location · Cached: HH:mm" so the age is clear.
     * If there is no cached data at all, a plain "No location" placeholder is
     * rendered instead.
     *
     * The card still opens the PWA and the pill still triggers a refresh, so the
     * user can retry once GPS comes back.
     */
    private fun renderNoLocation(ctx: Context, mgr: AppWidgetManager, id: Int, cfg: WidgetConfig) {
        val options: Bundle = mgr.getAppWidgetOptions(id)
        val widthPx = sizePx(ctx, options, AppWidgetManager.OPTION_APPWIDGET_MAX_WIDTH, 350)
        val heightPx = sizePx(ctx, options, AppWidgetManager.OPTION_APPWIDGET_MAX_HEIGHT, 160)
        val views = RemoteViews(ctx.packageName, R.layout.widget_rain)

        val cached = readCached(ctx, id)
        val response = cached?.first
        val cachedAtMs = cached?.second ?: 0L

        if (response != null) {
            // GPS isn't available right now but we have a recent forecast — show
            // it so the widget doesn't go blank, and label the timestamp clearly.
            views.setImageViewResource(R.id.condition, conditionDrawable(response.condition))
            views.setTextViewText(
                R.id.location,
                response.location.description.ifBlank { ctx.getString(R.string.state_no_location) },
            )
            val alarmData = response.buienalarm?.data ?: emptyList()
            val radarData = response.buienradar?.data ?: emptyList()
            val alarmMessage = response.buienalarm?.desc?.takeIf { it.isNotBlank() && it != "Buienalarm" }
            val hasNowcast = ChartRenderer.hasNowcast(alarmData, radarData)
            val dryWindow = hasNowcast && ChartRenderer.isDryWindow(alarmData, radarData)
            val data = ChartRenderer.Data(
                buienalarm = alarmData,
                buienradar = radarData,
                tempNow = response.temperature.now,
                tempEnd = response.temperature.end,
                conditionLabel = conditionHuman(response.condition),
                microNow = ChartRenderer.Micro(
                    feels = response.feelsLike.now,
                    windArrow = windArrow(response.wind.now.directionDeg),
                    windKmh = response.wind.now.speedKmh,
                    uv = response.uvIndex.now,
                ),
                microEnd = ChartRenderer.Micro(
                    feels = response.feelsLike.end,
                    windArrow = windArrow(response.wind.end.directionDeg),
                    windKmh = response.wind.end.speedKmh,
                    uv = response.uvIndex.end,
                ),
                sunEvents = response.sun.mapNotNull { ev ->
                    runCatching {
                        ChartRenderer.SunEvent(ev.kind, java.time.OffsetDateTime.parse(ev.time).toInstant())
                    }.getOrNull()
                },
                dryHeadline = alarmMessage,
            )
            views.setImageViewBitmap(R.id.chart, ChartRenderer.render(ctx, widthPx, heightPx, data))
            views.setTextViewText(
                R.id.updated,
                "↻  " + ctx.getString(R.string.no_location_cached_at, formatClock(cachedAtMs)),
            )
            views.setTextViewText(
                R.id.peak,
                when {
                    !hasNowcast -> ctx.getString(R.string.state_no_data)
                    dryWindow -> ""
                    else -> alarmMessage ?: peakLabel(alarmData, radarData)
                },
            )
        } else {
            // No cached data — show an honest empty state.
            views.setImageViewResource(R.id.condition, R.drawable.ic_cond_clear)
            views.setTextViewText(R.id.location, ctx.getString(R.string.state_no_location))
            views.setImageViewBitmap(
                R.id.chart,
                ChartRenderer.renderMessage(ctx, widthPx, heightPx, ctx.getString(R.string.state_no_location)),
            )
            views.setTextViewText(R.id.updated, "↻  " + ctx.getString(R.string.state_no_location))
            views.setTextViewText(R.id.peak, "")
        }

        wireClicks(ctx, views, id, cfg.serverUrl, null, null, null)
        mgr.updateAppWidget(id, views)
    }

    /** Wires both tap targets: the card opens the hourly forecast for this
     *  widget's resolved location, and the timestamp pill triggers a refresh
     *  via the non-exported [ManualRefreshReceiver]. */
    private fun wireClicks(
        ctx: Context,
        views: RemoteViews,
        id: Int,
        serverUrl: String,
        lat: Double?,
        lon: Double?,
        nameQ: String?,
    ) {
        val openIntent = Intent(Intent.ACTION_VIEW, buildOpenUri(serverUrl, lat, lon, nameQ)).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK
        }
        val openPi = PendingIntent.getActivity(
            ctx, id, openIntent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        views.setOnClickPendingIntent(R.id.root, openPi)

        val refreshIntent = Intent(ctx, ManualRefreshReceiver::class.java).apply {
            action = ManualRefreshReceiver.ACTION_MANUAL_REFRESH
            putExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, id)
        }
        val refreshPi = PendingIntent.getBroadcast(
            ctx, id, refreshIntent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        views.setOnClickPendingIntent(R.id.updated, refreshPi)
    }

    /** Builds the deep link the card opens: the hourly forecast page anchored
     *  to the widget's location (lat/lon, else name, else server IP). */
    private fun buildOpenUri(serverUrl: String, lat: Double?, lon: Double?, name: String?): Uri {
        val base = serverUrl.trimEnd('/')
        val b = Uri.parse("$base/hourly").buildUpon()
        if (lat != null && lon != null) {
            b.appendQueryParameter("lat", lat.toString())
            b.appendQueryParameter("lon", lon.toString())
        } else if (!name.isNullOrBlank()) {
            b.appendQueryParameter("name", name)
        }
        return b.build()
    }

    /**
     * Per-corner micro stats: a single short line carrying feels-like,
     * wind, UV, and rain probability. Compact format so two of these
     * (now + +2h) sit at the bottom corners next to the temp numbers
     * without clashing in a 4x2 widget.
     */
    /** "max 1.4 mm/h · heavy" — peak rain across the chart window with a
     *  descriptor. Empty when both providers are below the dry threshold. */
    private fun peakLabel(
        alarm: List<net.surfly.weather.widget.net.PointDto>,
        radar: List<net.surfly.weather.widget.net.PointDto>,
    ): String {
        val peak = maxOf(
            alarm.maxOfOrNull { it.value } ?: 0.0,
            radar.maxOfOrNull { it.value } ?: 0.0,
        )
        if (peak < 0.05) return ""
        val intensity = when {
            peak < 0.5 -> "drizzle"
            peak < 2.0 -> "moderate"
            peak < 5.0 -> "heavy"
            else -> "downpour"
        }
        return "max %.1f mm/h · %s".format(peak, intensity)
    }

    /** Wind FROM `dirFromN` (deg) → arrow showing where it's BLOWING TO. */
    private fun windArrow(dirFromN: Int): String {
        val toDeg = ((dirFromN + 180) % 360 + 360) % 360
        val sector = ((toDeg + 22) / 45) % 8
        return when (sector) {
            0 -> "↑"
            1 -> "↗"
            2 -> "→"
            3 -> "↘"
            4 -> "↓"
            5 -> "↙"
            6 -> "←"
            else -> "↖"
        }
    }

    private fun conditionDrawable(token: String): Int = when (token) {
        "clear" -> R.drawable.ic_cond_clear
        "partly_cloudy" -> R.drawable.ic_cond_partly_cloudy
        "overcast" -> R.drawable.ic_cond_overcast
        "fog" -> R.drawable.ic_cond_fog
        "drizzle" -> R.drawable.ic_cond_drizzle
        "rain" -> R.drawable.ic_cond_rain
        "snow" -> R.drawable.ic_cond_snow
        "thunderstorm" -> R.drawable.ic_cond_thunderstorm
        else -> R.drawable.ic_cond_clear
    }

    private fun conditionHuman(token: String): String = when (token) {
        "clear" -> "Clear"
        "partly_cloudy" -> "Partly cloudy"
        "overcast" -> "Overcast"
        "fog" -> "Fog"
        "drizzle" -> "Drizzle"
        "rain" -> "Rain"
        "snow" -> "Snow"
        "thunderstorm" -> "Thunderstorm"
        else -> "Clear"
    }

    private fun showRefreshing(ctx: Context, mgr: AppWidgetManager, id: Int) {
        val views = RemoteViews(ctx.packageName, R.layout.widget_rain)
        views.setTextViewText(R.id.updated, "↻  " + ctx.getString(R.string.updating))
        mgr.partiallyUpdateAppWidget(id, views)
    }

    /** Haversine — true when the two points are far enough apart that the rain
     *  nowcast cell would likely differ. The radar/alarm services serve cells
     *  on the order of ~1 km, so 500 m is a comfortable threshold that doesn't
     *  re-fetch on every GPS jitter. */
    private fun hasMoved(lat0: Double, lon0: Double, lat1: Double, lon1: Double): Boolean {
        val rEarthM = 6_371_000.0
        val dLat = Math.toRadians(lat1 - lat0)
        val dLon = Math.toRadians(lon1 - lon0)
        val a = Math.sin(dLat / 2).let { it * it } +
            Math.cos(Math.toRadians(lat0)) * Math.cos(Math.toRadians(lat1)) *
            Math.sin(dLon / 2).let { it * it }
        val c = 2 * Math.atan2(Math.sqrt(a), Math.sqrt(1 - a))
        return rEarthM * c > MOVED_THRESHOLD_M
    }

    private fun cacheJson(ctx: Context, id: Int, body: String) {
        File(ctx.cacheDir, CACHE_PREFIX + id + ".json").writeText(body)
    }

    /** Returns the cached response paired with the cache file's last-modified
     *  time (epoch millis), or null when there's nothing usable cached. */
    private fun readCached(ctx: Context, id: Int): Pair<GlanceResponse, Long>? {
        val f = File(ctx.cacheDir, CACHE_PREFIX + id + ".json")
        if (!f.exists()) return null
        val resp = runCatching {
            json.decodeFromString(GlanceResponse.serializer(), f.readText())
        }.getOrNull() ?: return null
        return resp to f.lastModified()
    }

    private fun formatClock(epochMs: Long): String =
        Instant.ofEpochMilli(epochMs).atZone(ZoneId.systemDefault()).format(updatedFmt)

    private fun errorLabel(ctx: Context, result: FetchResult): String = when (result) {
        is FetchResult.Ok -> ""
        is FetchResult.Err -> when (result.kind) {
            ErrKind.UNREACHABLE -> ctx.getString(R.string.state_offline)
            ErrKind.TIMEOUT -> ctx.getString(R.string.state_timeout)
            ErrKind.SERVER -> ctx.getString(R.string.state_server_error) + " " + (result.httpStatus ?: "")
            ErrKind.BAD_RESPONSE -> ctx.getString(R.string.state_bad_response)
        }
    }

    private fun sizePx(ctx: Context, opts: Bundle, key: String, fallbackDp: Int): Int {
        val dp = opts.getInt(key, fallbackDp).takeIf { it > 0 } ?: fallbackDp
        val px = (dp * ctx.resources.displayMetrics.density).toInt()
        return px.coerceIn(64, 1024)
    }
}
