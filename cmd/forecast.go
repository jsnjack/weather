package cmd

import (
	"time"
)

type ForecastType int // ForecastType represents the type of forecast
const (
	Temperature2mForecast ForecastType = iota
	PrecipitationForecast
	PrecipitationProbabilityForecast
	WindSpeed10mForecast
	WindDirection10mForecast
	ApparentTemperatureForecast
)

var ForecastTypeToString = map[ForecastType]string{
	Temperature2mForecast:            "Temperature",
	PrecipitationForecast:            "Precipitation",
	PrecipitationProbabilityForecast: "Precipitation probability",
	WindSpeed10mForecast:             "Wind speed",
	WindDirection10mForecast:         "Wind direction",
	ApparentTemperatureForecast:      "Apparent temperature",
}

var ForecastTypeToUnit = map[ForecastType]string{
	Temperature2mForecast:            "°C",
	PrecipitationForecast:            "mm",
	PrecipitationProbabilityForecast: "%",
	WindSpeed10mForecast:             "km/h",
	WindDirection10mForecast:         "°",
	ApparentTemperatureForecast:      "°C",
}

func (ft ForecastType) String() string {
	if str, ok := ForecastTypeToString[ft]; ok {
		return str
	}
	return "Unknown"
}

func (ft ForecastType) Unit() string {
	if unit, ok := ForecastTypeToUnit[ft]; ok {
		return unit
	}
	return ""
}

type ForecastDataPoint struct {
	Time  time.Time
	Value float64
}

type Forecast struct {
	Data []ForecastDataPoint
	Desc string
	Type ForecastType
}
