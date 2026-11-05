package net.surfly.weather.widget.net

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class LocationDto(
    val description: String = "",
    val latitude: Double = 0.0,
    val longitude: Double = 0.0,
)

@Serializable
data class PointDto(
    val time: String,
    val value: Double,
)

@Serializable
data class SeriesDto(
    val data: List<PointDto> = emptyList(),
    val desc: String? = null,
    val type: Int = 0,
)

// Note: the server JSON key is the misspelled "buineradar" — match it exactly.
@Serializable
data class RainResponse(
    val location: LocationDto = LocationDto(),
    val buienalarm: SeriesDto? = null,
    @SerialName("buineradar") val buienradar: SeriesDto? = null,
)

@Serializable
data class TemperatureDto(
    val now: Int = 0,
    val end: Int = 0,
)

@Serializable
data class WindDto(
    @SerialName("speed_kmh") val speedKmh: Int = 0,
    @SerialName("direction_deg") val directionDeg: Int = 0,
)

@Serializable
data class WindPairDto(
    val now: WindDto = WindDto(),
    val end: WindDto = WindDto(),
)

@Serializable
data class SunEventDto(
    val kind: String = "",   // "sunrise" | "sunset"
    val time: String = "",   // RFC3339 with offset
)

@Serializable
data class GlanceResponse(
    val location: LocationDto = LocationDto(),
    val buienalarm: SeriesDto? = null,
    @SerialName("buineradar") val buienradar: SeriesDto? = null,
    val temperature: TemperatureDto = TemperatureDto(),
    @SerialName("feels_like") val feelsLike: TemperatureDto = TemperatureDto(),
    val wind: WindPairDto = WindPairDto(),
    @SerialName("uv_index") val uvIndex: TemperatureDto = TemperatureDto(),
    val sun: List<SunEventDto> = emptyList(),
    val sunset: String? = null,
    val condition: String = "clear",
    /** Now is after sunset or before sunrise. */
    val night: Boolean = false,
    /** The next full hours, for the dry lane's hour strip. Empty from older servers. */
    val hours: List<GlanceHourDto> = emptyList(),
    /** First forecast hour with rain from two hours ahead; null when none is forecast. */
    @SerialName("next_rain") val nextRain: NextRainDto? = null,
)

@Serializable
data class GlanceHourDto(
    val time: String = "",   // RFC3339 with offset, local zone
    val condition: String = "clear",
    val temperature: Int = 0,
    val night: Boolean = false,
)

@Serializable
data class NextRainDto(
    val time: String = "",   // RFC3339 with offset, local zone
    val mm: Double = 0.0,
)
