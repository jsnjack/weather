package net.surfly.weather.widget.net

import android.graphics.BitmapFactory
import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import java.time.Instant
import java.util.concurrent.TimeUnit

/** One theme's map: the PNG bytes (kept for the offline store) and the verdict. */
class RadarFetch(val verdict: RadarVerdict, val png: ByteArray?)

/** Light and dark maps for the same place. */
class RadarPair(val light: RadarFetch, val dark: RadarFetch)

internal object RadarApi {
    private const val MAX_BYTES = 2_097_152L
    private val client = OkHttpClient.Builder()
        .callTimeout(20, TimeUnit.SECONDS)
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .build()

    /** Fetches the light and dark maps in parallel; the launcher picks one by theme. */
    suspend fun fetchBoth(serverUrl: String, lat: Double, lon: Double, label: String?): RadarPair = coroutineScope {
        val light = async(Dispatchers.IO) { fetch(serverUrl, lat, lon, label, dark = false) }
        val dark = async(Dispatchers.IO) { fetch(serverUrl, lat, lon, label, dark = true) }
        RadarPair(light.await(), dark.await())
    }

    private fun fetch(serverUrl: String, lat: Double, lon: Double, label: String?, dark: Boolean): RadarFetch {
        val url = "${serverUrl.trimEnd('/')}/api/v1/radar/local".toHttpUrlOrNull()?.newBuilder()
            ?.addQueryParameter("lat", lat.toString())
            ?.addQueryParameter("lon", lon.toString())
            ?.apply { if (!label.isNullOrBlank()) addQueryParameter("label", label) }
            ?.addQueryParameter("theme", if (dark) "dark" else "light")
            ?.addQueryParameter("always", "1")
            ?.addQueryParameter("size", "512")
            ?.addQueryParameter("outline", "30")
            ?.build() ?: return RadarFetch(RadarVerdict.Failed, null)
        return try {
            client.newCall(Request.Builder().url(url).build()).execute().use { response ->
                val verdict = RadarPolicy.classify(response.code, { response.header(it) }, Instant.now())
                if (verdict !is RadarVerdict.Show) {
                    Log.d("RainWidget", "Local radar: HTTP ${response.code}")
                    return RadarFetch(verdict, null)
                }
                val body = response.body ?: return RadarFetch(RadarVerdict.Failed, null)
                if (body.contentLength() > MAX_BYTES) return RadarFetch(RadarVerdict.Failed, null)
                val source = body.source()
                source.request(MAX_BYTES + 1)
                if (source.buffer.size > MAX_BYTES) return RadarFetch(RadarVerdict.Failed, null)
                val bytes = source.buffer.readByteArray()
                if (!isMapImage(bytes)) return RadarFetch(RadarVerdict.Failed, null)
                RadarFetch(verdict, bytes)
            }
        } catch (e: java.io.IOException) {
            Log.d("RainWidget", "Local radar request failed", e)
            RadarFetch(RadarVerdict.Failed, null)
        }
    }

    /** Older servers ignore `size` and send 256 px; both are square maps. */
    fun isMapImage(bytes: ByteArray): Boolean {
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
        return bounds.outWidth == bounds.outHeight && (bounds.outWidth == 256 || bounds.outWidth == 512)
    }
}
