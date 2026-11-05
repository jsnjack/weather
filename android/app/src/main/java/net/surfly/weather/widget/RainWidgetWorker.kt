package net.surfly.weather.widget

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.os.Bundle
import android.util.Log
import android.util.SizeF
import androidx.core.content.edit
import androidx.core.net.toUri
import androidx.work.CoroutineWorker
import androidx.work.WorkerParameters
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.withContext
import net.surfly.weather.widget.location.LocationProvider
import net.surfly.weather.widget.net.ErrKind
import net.surfly.weather.widget.net.FetchResult
import net.surfly.weather.widget.net.GlanceApi
import net.surfly.weather.widget.net.GlanceResponse
import net.surfly.weather.widget.net.RadarApi
import net.surfly.weather.widget.net.RadarPair
import net.surfly.weather.widget.net.RadarPolicy
import net.surfly.weather.widget.net.RadarVerdict
import net.surfly.weather.widget.render.Lane
import net.surfly.weather.widget.render.Links
import net.surfly.weather.widget.render.MapState
import net.surfly.weather.widget.render.WidgetContent
import net.surfly.weather.widget.render.WidgetRenderer
import java.time.Instant

class RainWidgetWorker(
    appContext: Context,
    params: WorkerParameters,
) : CoroutineWorker(appContext, params) {

    companion object {
        const val KEY_WIDGET_ID = "appWidgetId"

        /** Redraw from the store without network, e.g. after a resize. */
        const val KEY_RENDER_ONLY = "renderOnly"
        private const val TAG = "RainWidget"
        private const val MOVED_THRESHOLD_M = 500.0
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
        val renderOnly = inputData.getBoolean(KEY_RENDER_ONLY, false)
        for (id in ids) {
            if (renderOnly && WidgetUpdater.renderStored(ctx, mgr, id, pill = null)) continue
            refreshOne(ctx, mgr, id)
        }
        Result.success()
    }

    private suspend fun refreshOne(ctx: Context, mgr: AppWidgetManager, id: Int) {
        val cfg = WidgetPrefs.load(ctx, id)
        // Feedback before any GPS or network work: redraw the stored state
        // with "Refreshing…" in the pill. Same layout, so nothing jumps.
        WidgetUpdater.renderStored(ctx, mgr, id, pill = ctx.getString(R.string.pill_refreshing))
        when (cfg.mode) {
            LocationMode.AUTO -> refreshAuto(ctx, mgr, id, cfg)
            LocationMode.NAME -> {
                val name = cfg.name.ifBlank { null }
                if (name == null) WidgetUpdater.renderNoLocation(ctx, mgr, id, cfg)
                else fetchAndRender(ctx, mgr, id, cfg, lat = null, lon = null, nameQ = name)
            }
            LocationMode.COORDS -> fetchAndRender(ctx, mgr, id, cfg, lat = cfg.lat, lon = cfg.lon, nameQ = null)
            LocationMode.IP -> fetchAndRender(ctx, mgr, id, cfg, lat = null, lon = null, nameQ = null)
        }
    }

    /**
     * Two-pass refresh for Auto mode: render with the last stored fix right
     * away, and re-fetch only if the fresh fix (which can take ~8 s) moved
     * far enough to change the nowcast. Both passes draw the same layout.
     */
    private suspend fun refreshAuto(ctx: Context, mgr: AppWidgetManager, id: Int, cfg: WidgetConfig) = coroutineScope {
        val provider = LocationProvider(ctx)
        val stored = provider.lastSavedFix()
        val freshDeferred = async { provider.freshFix() }
        if (stored != null) {
            fetchAndRender(ctx, mgr, id, cfg, lat = stored.first, lon = stored.second, nameQ = null)
        }
        val fresh = freshDeferred.await()
        if (fresh == null) {
            if (stored == null) WidgetUpdater.renderNoLocation(ctx, mgr, id, cfg)
            return@coroutineScope
        }
        if (stored == null || RadarPolicy.distanceM(stored.first, stored.second, fresh.lat, fresh.lon) > MOVED_THRESHOLD_M) {
            fetchAndRender(ctx, mgr, id, cfg, lat = fresh.lat, lon = fresh.lon, nameQ = null)
        }
    }

    /**
     * Fetches the forecast and both map themes, then draws the widget once.
     * When the coordinates are known up front the maps load in parallel with
     * the forecast; for place-name and IP modes they follow it.
     */
    private suspend fun fetchAndRender(
        ctx: Context,
        mgr: AppWidgetManager,
        id: Int,
        cfg: WidgetConfig,
        lat: Double?,
        lon: Double?,
        nameQ: String?,
    ) = coroutineScope {
        val glance = async { GlanceApi.fetch(cfg.serverUrl, lat, lon, nameQ) }
        val early = if (lat != null && lon != null) async { RadarApi.fetchBoth(cfg.serverUrl, lat, lon, null) } else null
        val result = glance.await()

        val response: GlanceResponse?
        val pill: String
        when (result) {
            is FetchResult.Ok -> {
                WidgetStore.saveGlance(ctx, id, result.rawJson)
                response = result.response
                pill = ctx.getString(R.string.pill_updated, WidgetRenderer.clock(Instant.now()))
            }
            is FetchResult.Err -> {
                val cached = WidgetStore.loadGlance(ctx, id)
                response = cached?.first
                pill = if (cached != null) {
                    ctx.getString(R.string.pill_cached, WidgetRenderer.clock(Instant.ofEpochMilli(cached.second)))
                } else {
                    ctx.getString(R.string.pill_retry)
                }
            }
        }

        val map: MapState = if (response == null) {
            early?.cancel()
            MapState.None
        } else {
            val loc = response.location
            val pair = early?.await()
                ?: if (result is FetchResult.Ok) RadarApi.fetchBoth(cfg.serverUrl, loc.latitude, loc.longitude, loc.description) else null
            mapState(ctx, id, pair, loc.latitude, loc.longitude)
        }

        val content = if (response != null) {
            WidgetUpdater.content(ctx, response, pill, map)
        } else {
            WidgetContent(
                location = "—",
                pill = pill,
                response = null,
                laneTitle = errorLabel(ctx, result),
                lane = Lane.Note(ctx.getString(R.string.note_retry)),
                map = map,
                links = null,
            )
        }
        WidgetUpdater.push(ctx, mgr, id, content, cfg, lat, lon, nameQ, response)
    }

    /** Turns a fetch into what the map slot shows, storing good maps and reusing them offline. */
    private fun mapState(ctx: Context, id: Int, pair: RadarPair?, lat: Double, lon: Double): MapState {
        val light = pair?.light
        val dark = pair?.dark
        val shown = listOfNotNull(light, dark).filter { it.verdict is RadarVerdict.Show && it.png != null }
        if (shown.isNotEmpty()) {
            // If one theme failed, show the other in both rather than nothing.
            val l = shown.firstOrNull { it === light } ?: shown.first()
            val d = shown.firstOrNull { it === dark } ?: shown.first()
            val meta = ((l.verdict as RadarVerdict.Show)).meta
            val lb = WidgetStore.decode(l.png!!)
            val db = WidgetStore.decode(d.png!!)
            if (lb != null && db != null) {
                runCatching { WidgetStore.saveMap(ctx, id, l.png, d.png, meta, lat, lon) }
                    .onFailure { Log.d(TAG, "Couldn't store the radar map", it) }
                return MapState.Live(MapImages(lb, db), meta)
            }
        }
        // The server answered, but without a usable map: say so instead of
        // showing an older stored one.
        val verdicts = listOfNotNull(light, dark).map { it.verdict }
        if (RadarVerdict.Stale in verdicts) return MapState.Unavailable
        if (RadarVerdict.NoMap in verdicts) return MapState.None
        val stored = WidgetStore.loadMap(ctx, id)
        if (stored != null && RadarPolicy.sameArea(stored.lat, stored.lon, lat, lon)) {
            return MapState.Stored(stored.images, stored.meta)
        }
        return MapState.None
    }

    private fun errorLabel(ctx: Context, result: FetchResult): String = when (result) {
        is FetchResult.Ok -> ""
        is FetchResult.Err -> when (result.kind) {
            ErrKind.UNREACHABLE -> ctx.getString(R.string.state_offline)
            ErrKind.TIMEOUT -> ctx.getString(R.string.state_timeout)
            ErrKind.SERVER -> ctx.getString(R.string.state_server_error, result.httpStatus?.toString() ?: "")
            ErrKind.BAD_RESPONSE -> ctx.getString(R.string.state_bad_response)
        }
    }
}

/** Builds widget content, draws it at every size the launcher uses, and keeps the picker preview current. */
object WidgetUpdater {
    private const val TAG = "RainWidget"
    private const val PREVIEW_PREFS = "preview"
    private const val PREVIEW_INTERVAL_MS = 6 * 60 * 60 * 1000L

    fun content(ctx: Context, response: GlanceResponse, pill: String, map: MapState, links: Links? = null): WidgetContent {
        val (title, lane) = WidgetRenderer.lane(ctx, response, map)
        return WidgetContent(
            location = response.location.description.ifBlank { ctx.getString(R.string.widget_label) },
            pill = pill,
            response = response,
            laneTitle = title,
            lane = lane,
            map = map,
            links = links,
        )
    }

    /** Draws the stored forecast and map; false when nothing is stored yet. */
    fun renderStored(ctx: Context, mgr: AppWidgetManager, id: Int, pill: String?): Boolean {
        val content = storedContent(ctx, id, pill) ?: return false
        val cfg = WidgetPrefs.load(ctx, id)
        val loc = content.response?.location
        update(ctx, mgr, id, content, cfg, loc?.latitude, loc?.longitude, null)
        return true
    }

    /** The stored forecast and map as widget content, for redraws and the settings preview. */
    fun storedContent(ctx: Context, id: Int, pill: String?): WidgetContent? {
        val (response, savedAt) = WidgetStore.loadGlance(ctx, id) ?: return null
        val stored = WidgetStore.loadMap(ctx, id)
        val loc = response.location
        // A redraw (refreshing, resize, theme change) must look exactly like
        // the last refresh, so a map that is still current draws as live;
        // only an old one gets the "Radar HH:mm" label.
        val now = Instant.now()
        val map = when {
            stored == null || !RadarPolicy.sameArea(stored.lat, stored.lon, loc.latitude, loc.longitude) -> MapState.None
            RadarPolicy.isFresh(stored.meta.observedAt ?: Instant.ofEpochMilli(stored.savedAtMs), now) ->
                MapState.Live(stored.images, stored.meta)
            else -> MapState.Stored(stored.images, stored.meta)
        }
        val label = pill ?: ctx.getString(R.string.pill_updated, WidgetRenderer.clock(Instant.ofEpochMilli(savedAt)))
        return content(ctx, response, label, map)
    }

    /**
     * Auto mode without any fix. Shows the stored forecast when there is one
     * (GPS is often slow right after unlock); otherwise an empty state that
     * still has every box in place. It never falls back to IP location.
     */
    fun renderNoLocation(ctx: Context, mgr: AppWidgetManager, id: Int, cfg: WidgetConfig) {
        if (renderStored(ctx, mgr, id, pill = ctx.getString(R.string.pill_retry))) return
        val content = WidgetContent(
            location = ctx.getString(R.string.state_no_location),
            pill = ctx.getString(R.string.pill_retry),
            response = null,
            laneTitle = ctx.getString(R.string.state_no_location),
            lane = Lane.Note(ctx.getString(R.string.note_no_location)),
            map = MapState.NoLocation,
            links = null,
        )
        update(ctx, mgr, id, content, cfg, null, null, null)
    }

    fun push(
        ctx: Context,
        mgr: AppWidgetManager,
        id: Int,
        content: WidgetContent,
        cfg: WidgetConfig,
        lat: Double?,
        lon: Double?,
        nameQ: String?,
        response: GlanceResponse?,
    ) {
        update(ctx, mgr, id, content, cfg, lat, lon, nameQ)
        if (response != null) maybeUpdatePreview(ctx, mgr, content)
    }

    private fun update(
        ctx: Context,
        mgr: AppWidgetManager,
        id: Int,
        content: WidgetContent,
        cfg: WidgetConfig,
        lat: Double?,
        lon: Double?,
        nameQ: String?,
    ) {
        val linked = WidgetContent(
            content.location, content.pill, content.response, content.laneTitle, content.lane, content.map,
            links(ctx, id, cfg, lat, lon, nameQ, content.response),
        )
        val views = WidgetRenderer.build(ctx, linked, sizes(mgr.getAppWidgetOptions(id)))
        mgr.updateAppWidget(id, views)
    }

    /** The sizes the launcher shows this widget at (portrait, landscape). */
    fun sizes(options: Bundle): List<SizeF> {
        val sizes = options.getParcelableArrayList(AppWidgetManager.OPTION_APPWIDGET_SIZES, SizeF::class.java)
        if (!sizes.isNullOrEmpty()) return sizes
        val w = options.getInt(AppWidgetManager.OPTION_APPWIDGET_MIN_WIDTH, 0)
        val h = options.getInt(AppWidgetManager.OPTION_APPWIDGET_MAX_HEIGHT, 0)
        return if (w > 0 && h > 0) listOf(SizeF(w.toFloat(), h.toFloat())) else emptyList()
    }

    private fun links(
        ctx: Context,
        id: Int,
        cfg: WidgetConfig,
        lat: Double?,
        lon: Double?,
        nameQ: String?,
        response: GlanceResponse?,
    ): Links {
        val base = cfg.serverUrl.trimEnd('/')
        // The glance page: now, the next two hours, today and 13 days in one scroll.
        val page = "$base/glance".toUri().buildUpon()
        if (lat != null && lon != null) {
            page.appendQueryParameter("lat", lat.toString()).appendQueryParameter("lon", lon.toString())
            response?.location?.description?.takeIf { it.isNotBlank() }?.let { page.appendQueryParameter("label", it) }
        } else if (!nameQ.isNullOrBlank()) {
            page.appendQueryParameter("name", nameQ)
        }
        val open = PendingIntent.getActivity(
            ctx, id, Intent(Intent.ACTION_VIEW, page.build()).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val refresh = PendingIntent.getBroadcast(
            ctx, id,
            Intent(ctx, ManualRefreshReceiver::class.java)
                .setAction(ManualRefreshReceiver.ACTION_MANUAL_REFRESH)
                .putExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, id),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )
        val radar = response?.location?.let { loc ->
            // Plays the local forecast on the glance page, below everything else.
            val uri = "$base/glance".toUri().buildUpon()
                .appendQueryParameter("lat", loc.latitude.toString())
                .appendQueryParameter("lon", loc.longitude.toString())
                .appendQueryParameter("label", loc.description)
                .appendQueryParameter("radar", "play")
                .fragment("radar-player")
                .build()
            PendingIntent.getActivity(
                ctx, id + 100_000, Intent(Intent.ACTION_VIEW, uri).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            )
        }
        return Links(open, refresh, radar)
    }

    /**
     * Shows the real widget in the widget picker (Android 15 API). The system
     * rate-limits these calls, so update at most every few hours.
     */
    private fun maybeUpdatePreview(ctx: Context, mgr: AppWidgetManager, content: WidgetContent) {
        val prefs = ctx.getSharedPreferences(PREVIEW_PREFS, Context.MODE_PRIVATE)
        val now = System.currentTimeMillis()
        if (now - prefs.getLong("last", 0L) < PREVIEW_INTERVAL_MS) return
        val preview = WidgetContent(
            content.location, content.pill, content.response, content.laneTitle, content.lane, content.map, null,
        )
        val views = WidgetRenderer.build(ctx, preview, listOf(SizeF(384f, 220f)))
        val ok = runCatching {
            mgr.setWidgetPreview(
                ComponentName(ctx, RainWidgetProvider::class.java),
                android.appwidget.AppWidgetProviderInfo.WIDGET_CATEGORY_HOME_SCREEN,
                views,
            )
        }.onFailure { Log.d(TAG, "Widget preview not updated", it) }.getOrDefault(false)
        if (ok) prefs.edit { putLong("last", now) }
    }
}
