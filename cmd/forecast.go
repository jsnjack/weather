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
	Temperature2mForecast: "°C",
	// Both Buienalarm (precipitationrate) and Buienradar (dataValue from
	// RainHistoryForecast) report precipitation in mm/h, so we display the
	// rate unit rather than a bare "mm".
	PrecipitationForecast:            "mm/h",
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
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
}

type Forecast struct {
	nowcastMessage string
	Data           []ForecastDataPoint `json:"data"`
	Desc           string              `json:"desc"`
	Type           ForecastType        `json:"type"`
	// Source names the provider that drew this series, e.g. "Buienalarm" or
	// "DWD": the chart's second line comes from a different radar per country.
	Source string `json:"source"`
	// Estimate marks a series that isn't plain radar, such as Buienalarm's
	// worldwide product outside western Europe.
	Estimate bool `json:"estimate,omitempty"`
}

// labelOr is the series name for chart keys: the source, marked when it is
// only an estimate, or fallback for a nil forecast or one without a source.
func (f *Forecast) labelOr(fallback string) string {
	if f == nil || f.Source == "" {
		return fallback
	}
	if f.Estimate {
		return f.Source + " (estimate)"
	}
	return f.Source
}
