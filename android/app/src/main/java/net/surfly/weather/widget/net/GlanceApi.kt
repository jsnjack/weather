package net.surfly.weather.widget.net

import kotlinx.serialization.json.Json
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import java.util.concurrent.TimeUnit

sealed class FetchResult {
    data class Ok(val response: GlanceResponse, val rawJson: String) : FetchResult()
    data class Err(val kind: ErrKind, val httpStatus: Int? = null) : FetchResult()
}

enum class ErrKind { UNREACHABLE, TIMEOUT, SERVER, BAD_RESPONSE }

object GlanceApi {
    private val json = Json { ignoreUnknownKeys = true; isLenient = true }

    private val client: OkHttpClient = OkHttpClient.Builder()
        .callTimeout(15, TimeUnit.SECONDS)
        .connectTimeout(5, TimeUnit.SECONDS)
        .readTimeout(10, TimeUnit.SECONDS)
        .retryOnConnectionFailure(true)
        .build()

    fun fetch(serverUrl: String, lat: Double?, lon: Double?, name: String?): FetchResult {
        val base = serverUrl.trimEnd('/')
        val url = "$base/api/v1/glance".toHttpUrlOrNull()?.newBuilder()
            ?.apply {
                if (lat != null && lon != null) {
                    addQueryParameter("lat", lat.toString())
                    addQueryParameter("lon", lon.toString())
                } else if (!name.isNullOrBlank()) {
                    addQueryParameter("name", name)
                }
            }?.build() ?: return FetchResult.Err(ErrKind.UNREACHABLE)

        val req = Request.Builder().url(url).get().build()
        return try {
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) {
                    return FetchResult.Err(ErrKind.SERVER, resp.code)
                }
                val body = resp.body?.string() ?: return FetchResult.Err(ErrKind.BAD_RESPONSE)
                val parsed = runCatching { json.decodeFromString(GlanceResponse.serializer(), body) }
                    .getOrElse { return FetchResult.Err(ErrKind.BAD_RESPONSE) }
                FetchResult.Ok(parsed, body)
            }
        } catch (e: java.net.SocketTimeoutException) {
            FetchResult.Err(ErrKind.TIMEOUT)
        } catch (e: java.net.UnknownHostException) {
            FetchResult.Err(ErrKind.UNREACHABLE)
        } catch (e: java.io.IOException) {
            FetchResult.Err(ErrKind.UNREACHABLE)
        }
    }
}
