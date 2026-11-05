package net.surfly.weather.widget

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import net.surfly.weather.widget.net.GlanceResponse
import net.surfly.weather.widget.net.RadarMeta
import java.io.File
import java.time.Instant

/** Light and dark versions of one map, decoded. */
class MapImages(val light: Bitmap, val dark: Bitmap)

/** The last map shown for a widget, with where and when it was drawn. */
class StoredMap(val images: MapImages, val meta: RadarMeta, val lat: Double, val lon: Double, val savedAtMs: Long)

/**
 * Per-widget disk cache: the last forecast JSON and the last light/dark map.
 * It lets the widget redraw without the network — while refreshing, after a
 * resize, and offline — so the layout never empties between updates.
 */
object WidgetStore {
    private const val GLANCE_PREFIX = "last_glance_"
    private const val RADAR_PREFIX = "last_radar_"
    private val json = Json { ignoreUnknownKeys = true; isLenient = true }

    @Serializable
    private data class RadarRecord(
        val observedAtMs: Long? = null,
        val unavailable: Boolean = false,
        val rainNearby: Boolean? = null,
        val outline: Boolean = false,
        val lat: Double = 0.0,
        val lon: Double = 0.0,
    )

    fun saveGlance(ctx: Context, id: Int, body: String) {
        File(ctx.cacheDir, "$GLANCE_PREFIX$id.json").writeText(body)
    }

    /** The cached forecast and the time it was saved (epoch millis). */
    fun loadGlance(ctx: Context, id: Int): Pair<GlanceResponse, Long>? {
        val f = File(ctx.cacheDir, "$GLANCE_PREFIX$id.json")
        if (!f.exists()) return null
        val resp = runCatching { json.decodeFromString(GlanceResponse.serializer(), f.readText()) }.getOrNull()
            ?: return null
        return resp to f.lastModified()
    }

    fun saveMap(ctx: Context, id: Int, light: ByteArray, dark: ByteArray, meta: RadarMeta, lat: Double, lon: Double) {
        File(ctx.cacheDir, "$RADAR_PREFIX${id}_light.png").writeBytes(light)
        File(ctx.cacheDir, "$RADAR_PREFIX${id}_dark.png").writeBytes(dark)
        val record = RadarRecord(meta.observedAt?.toEpochMilli(), meta.unavailable, meta.rainNearby, meta.outline, lat, lon)
        File(ctx.cacheDir, "$RADAR_PREFIX$id.json").writeText(json.encodeToString(RadarRecord.serializer(), record))
    }

    fun loadMap(ctx: Context, id: Int): StoredMap? {
        val metaFile = File(ctx.cacheDir, "$RADAR_PREFIX$id.json")
        if (!metaFile.exists()) return null
        val record = runCatching { json.decodeFromString(RadarRecord.serializer(), metaFile.readText()) }.getOrNull()
            ?: return null
        val light = decode(File(ctx.cacheDir, "$RADAR_PREFIX${id}_light.png")) ?: return null
        val dark = decode(File(ctx.cacheDir, "$RADAR_PREFIX${id}_dark.png")) ?: return null
        val meta = RadarMeta(record.observedAtMs?.let(Instant::ofEpochMilli), record.unavailable, record.rainNearby, record.outline)
        return StoredMap(MapImages(light, dark), meta, record.lat, record.lon, metaFile.lastModified())
    }

    fun decode(bytes: ByteArray): Bitmap? = BitmapFactory.decodeByteArray(bytes, 0, bytes.size)

    private fun decode(f: File): Bitmap? = if (f.exists()) BitmapFactory.decodeFile(f.path) else null

    fun clear(ctx: Context, id: Int) {
        listOf("$GLANCE_PREFIX$id.json", "$RADAR_PREFIX$id.json", "$RADAR_PREFIX${id}_light.png", "$RADAR_PREFIX${id}_dark.png")
            .forEach { File(ctx.cacheDir, it).delete() }
    }

    /** The most recently refreshed widget, for the settings preview of a new widget. */
    fun newestWidgetId(ctx: Context): Int? =
        ctx.cacheDir.listFiles { f -> f.name.startsWith(GLANCE_PREFIX) }
            ?.maxByOrNull { it.lastModified() }
            ?.name?.removePrefix(GLANCE_PREFIX)?.removeSuffix(".json")?.toIntOrNull()
}
