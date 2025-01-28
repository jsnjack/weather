package cmd

import (
	"time"
)

type ForecasePoint struct {
	Time          time.Time
	Precipitation float64
}

type Forecast struct {
	Temperature int
	Data        []*ForecasePoint
	Desc        string
}
