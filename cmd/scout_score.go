package cmd

import "math"

const (
	daytimeStartHour = 10
	daytimeEndHour   = 20  // exclusive — so window is 10:00..19:59
	rainThresholdMm  = 0.0 // any measurable precipitation disqualifies the day

	// Cycling-calibrated wind bands, in km/h of sustained 10m wind.
	windCalmKmh    = 10.0 // ≤10 feels like nothing on a bike
	windBreezyKmh  = 25.0 // 10–25 is noticeable but fine
	windWindyKmh   = 40.0 // 25–40 is a hard ride, especially as headwind
	windStrongKmh  = 60.0 // 40–60 is a fight
	gustDisqualify = 60.0 // any gust ≥60 km/h knocks out the day
)

// DayScore summarises a single day at a single sample point.
type DayScore struct {
	Disqualified     bool
	Reason           string // "RAIN", "GUST", "NODATA" — empty if ok
	Score            float64
	MaxTemp          float64
	MinTemp          float64
	TailwindAvg      float64 // km/h, positive = tailwind, negative = headwind
	MaxSustainedWind float64 // km/h, daytime max of sustained 10m wind
	MaxGust          float64
	MaxPrecip        float64
	BelowMinTemp     bool // true if MaxTemp < user's minTemp
}

// ScoreDay evaluates a day's daytime-hour weather against the chosen bearing.
// minTemp is the user-preferred lower bound; below it we penalize but do not
// disqualify (cold is tolerable in a way rain is not).
func ScoreDay(hourly []HourlyForecast, bearingDeg float64, minTemp float64) DayScore {
	ds := DayScore{
		MinTemp: math.MaxFloat64,
		MaxTemp: -math.MaxFloat64,
	}

	bearingRad := bearingDeg * math.Pi / 180
	tailwindSum := 0.0
	dayCount := 0

	for _, h := range hourly {
		hr := h.Time.Hour()
		if hr < daytimeStartHour || hr >= daytimeEndHour {
			continue
		}
		dayCount++

		if h.Temperature > ds.MaxTemp {
			ds.MaxTemp = h.Temperature
		}
		if h.Temperature < ds.MinTemp {
			ds.MinTemp = h.Temperature
		}
		if h.Precipitation > ds.MaxPrecip {
			ds.MaxPrecip = h.Precipitation
		}
		if h.WindGusts > ds.MaxGust {
			ds.MaxGust = h.WindGusts
		}
		if h.WindSpeed > ds.MaxSustainedWind {
			ds.MaxSustainedWind = h.WindSpeed
		}

		// Meteorological wind direction is where wind comes FROM. "Blows toward"
		// is from+180°. Tailwind = projection of that vector onto the heading:
		// -speed * cos(bearing - from).
		windFromRad := h.WindDirection * math.Pi / 180
		tailwindSum += -h.WindSpeed * math.Cos(bearingRad-windFromRad)
	}

	if dayCount == 0 {
		ds.Disqualified = true
		ds.Reason = "NODATA"
		return ds
	}
	ds.TailwindAvg = tailwindSum / float64(dayCount)
	ds.BelowMinTemp = ds.MaxTemp < minTemp

	if ds.MaxPrecip > rainThresholdMm {
		ds.Disqualified = true
		ds.Reason = "RAIN"
		return ds
	}
	if ds.MaxGust >= gustDisqualify {
		ds.Disqualified = true
		ds.Reason = "GUST"
		return ds
	}

	// Temperature contribution: flat bonus if comfortable, linear penalty below.
	var tempScore float64
	if ds.MaxTemp >= minTemp {
		tempScore = 10
	} else {
		tempScore = (ds.MaxTemp - minTemp) * 3 // negative
	}

	// Sustained-wind penalty, tuned for cycling: 0 up to calm, ramps up above.
	windPenalty := cyclingWindPenalty(ds.MaxSustainedWind)

	ds.Score = tempScore + ds.TailwindAvg - windPenalty
	return ds
}

// cyclingWindPenalty is the score cost of riding through max sustained wind
// of w km/h. 0 below the calm band, ramps up as wind crosses each threshold.
func cyclingWindPenalty(w float64) float64 {
	switch {
	case w <= windCalmKmh:
		return 0
	case w <= windBreezyKmh:
		return (w - windCalmKmh) * 0.3 // up to ~4.5 at 25
	case w <= windWindyKmh:
		return (windBreezyKmh-windCalmKmh)*0.3 + (w-windBreezyKmh)*0.8 // up to ~16 at 40
	default:
		return (windBreezyKmh-windCalmKmh)*0.3 + (windWindyKmh-windBreezyKmh)*0.8 + (w-windWindyKmh)*1.5
	}
}

// ScoreDayOmni scores a day at a static point with no heading — used by the
// heatmap. Same rain/gust disqualification rules; score is temperature comfort
// minus gust penalty, with no tailwind contribution.
func ScoreDayOmni(hourly []HourlyForecast, minTemp float64) DayScore {
	ds := DayScore{
		MinTemp: math.MaxFloat64,
		MaxTemp: -math.MaxFloat64,
	}
	dayCount := 0
	for _, h := range hourly {
		hr := h.Time.Hour()
		if hr < daytimeStartHour || hr >= daytimeEndHour {
			continue
		}
		dayCount++
		if h.Temperature > ds.MaxTemp {
			ds.MaxTemp = h.Temperature
		}
		if h.Temperature < ds.MinTemp {
			ds.MinTemp = h.Temperature
		}
		if h.Precipitation > ds.MaxPrecip {
			ds.MaxPrecip = h.Precipitation
		}
		if h.WindGusts > ds.MaxGust {
			ds.MaxGust = h.WindGusts
		}
		if h.WindSpeed > ds.MaxSustainedWind {
			ds.MaxSustainedWind = h.WindSpeed
		}
	}
	if dayCount == 0 {
		ds.Disqualified = true
		ds.Reason = "NODATA"
		return ds
	}
	ds.BelowMinTemp = ds.MaxTemp < minTemp

	if ds.MaxPrecip > rainThresholdMm {
		ds.Disqualified = true
		ds.Reason = "RAIN"
		return ds
	}
	if ds.MaxGust >= gustDisqualify {
		ds.Disqualified = true
		ds.Reason = "GUST"
		return ds
	}

	var tempScore float64
	if ds.MaxTemp >= minTemp {
		tempScore = 10
	} else {
		tempScore = (ds.MaxTemp - minTemp) * 3
	}
	ds.Score = tempScore - cyclingWindPenalty(ds.MaxSustainedWind)
	return ds
}

// TempBand classifies MaxTemp relative to minTemp: 3 = warm (≥minTemp+5),
// 2 = warm (min..min+5), 1 = cool (min-5..min), 0 = cold (below min-5).
func TempBand(maxTemp, minTemp float64) int {
	switch {
	case maxTemp >= minTemp+5:
		return 3
	case maxTemp >= minTemp:
		return 2
	case maxTemp >= minTemp-5:
		return 1
	default:
		return 0
	}
}

// WindBand classifies sustained wind (km/h) by cycling feel:
// 0 = calm (≤10), 1 = breezy (10–25), 2 = windy (25–40), 3 = strong (40–60).
func WindBand(sustainedWindKmh float64) int {
	switch {
	case sustainedWindKmh <= windCalmKmh:
		return 0
	case sustainedWindKmh <= windBreezyKmh:
		return 1
	case sustainedWindKmh <= windWindyKmh:
		return 2
	default:
		return 3
	}
}
