package net.surfly.weather.widget.net

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.util.Log
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import java.time.Instant
import java.util.concurrent.TimeUnit

internal data class RadarSnapshot(val bitmap: Bitmap, val observedAt: Instant)

internal object RadarApi {
    private const val MAX_BYTES = 1_048_576L
    private val client = OkHttpClient.Builder()
        .callTimeout(15, TimeUnit.SECONDS)
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(10, TimeUnit.SECONDS)
        .build()

    fun fetch(serverUrl: String, location: LocationDto, dark: Boolean): RadarSnapshot? {
        val url = "${serverUrl.trimEnd('/')}/api/v1/radar/local".toHttpUrlOrNull()?.newBuilder()
            ?.addQueryParameter("lat", location.latitude.toString())
            ?.addQueryParameter("lon", location.longitude.toString())
            ?.addQueryParameter("label", location.description)
            ?.addQueryParameter("theme", if (dark) "dark" else "light")
            ?.build() ?: return null
        return try {
            client.newCall(Request.Builder().url(url).build()).execute().use { response ->
                if (response.code == 204 || response.code == 404) return null
                if (!response.isSuccessful) {
                    Log.d("RainWidget", "Local radar unavailable: HTTP ${response.code}")
                    return null
                }
                val observed = response.header("X-Radar-Observed-At")?.let { Instant.parse(it) } ?: return null
                if (!RadarPolicy.isFresh(observed, Instant.now())) return null
                val body = response.body ?: return null
                if (body.contentLength() > MAX_BYTES) return null
                val source = body.source()
                source.request(MAX_BYTES + 1)
                if (source.buffer.size > MAX_BYTES) return null
                val bytes = source.buffer.readByteArray()
                val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
                BitmapFactory.decodeByteArray(bytes, 0, bytes.size, bounds)
                if (bounds.outWidth != 256 || bounds.outHeight != 256) return null
                val bitmap = BitmapFactory.decodeByteArray(bytes, 0, bytes.size) ?: return null
                RadarSnapshot(bitmap, observed)
            }
        } catch (e: java.io.IOException) {
            Log.d("RainWidget", "Local radar request failed", e)
            null
        } catch (e: java.time.format.DateTimeParseException) {
            Log.d("RainWidget", "Local radar timestamp is invalid", e)
            null
        }
    }
}
