package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/jsnjack/termplt"
	"github.com/spf13/cobra"
)

var FlagForecastDays int

var forecastCmd = &cobra.Command{
	Use:   "forecast",
	Short: "Multi-day daily outlook (temp hi/lo, rain, wind, UV)",
	Long: `forecast shows a day-by-day outlook up to 16 days — daily high/low and
feels-like temperature, total precipitation, wind, gusts, and peak UV index.
Mirrors the /forecast (14-day) web page so the CLI and browser stay in sync.`,
	RunE: runForecast,
}

func init() {
	rootCmd.AddCommand(forecastCmd)
	forecastCmd.Flags().IntVar(&FlagForecastDays, "days", 14, "number of days (3–16)")
}

func runForecast(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	days := FlagForecastDays
	if days < 3 || days > 16 {
		return fmt.Errorf("--days must be between 3 and 16")
	}

	loc, err := ResolveLocation()
	if err != nil {
		return err
	}

	prog := NewCLIProgress("daily forecast")
	prog.AddTotal(1)
	daily, err := GetOpenMeteoDailyRange(loc.Latitude, loc.Longitude, days)
	prog.Inc(1)
	prog.Finish()
	if err != nil || len(daily) == 0 {
		return fmt.Errorf("daily forecast: %w", err)
	}

	fmt.Printf(termplt.ColorBold+"%d-day forecast for %s"+termplt.ColorReset+
		"  ·  %s → %s\n\n",
		len(daily), loc.Description,
		daily[0].Date.Format("Mon 2 Jan"), daily[len(daily)-1].Date.Format("Mon 2 Jan"))

	renderForecastTempChart(daily)
	fmt.Println()
	renderForecastTable(daily)
	return nil
}

func renderForecastTempChart(daily []DailyAggregate) {
	fmt.Printf("%sHigh%s · %sLow%s (°C)\n",
		termplt.ColorRed, termplt.ColorReset, termplt.ColorBlue, termplt.ColorReset)
	chart := termplt.NewLineChart()
	x := make([]float64, len(daily))
	hi := make([]float64, len(daily))
	lo := make([]float64, len(daily))
	for i, d := range daily {
		// Anchor each day's point at local noon so the line reads as one
		// sample per day, matching the web chart.
		noon := time.Date(d.Date.Year(), d.Date.Month(), d.Date.Day(), 12, 0, 0, 0, time.Local)
		x[i] = float64(noon.Unix())
		hi[i] = d.TempMax
		lo[i] = d.TempMin
	}
	chart.AddLine(x, hi, termplt.ColorRed)
	chart.AddLine(x, lo, termplt.ColorBlue)
	chart.SetXLabelAsTime("", "Mon 2")
	chart.SetYLabel("°C")
	fmt.Print(chart.String())
}

func renderForecastTable(daily []DailyAggregate) {
	b, rst := termplt.ColorBold, termplt.ColorReset

	// Global temperature span drives the ASCII range bars (mirrors the web bars).
	gMin, gMax := daily[0].TempMin, daily[0].TempMax
	for _, d := range daily {
		if d.TempMin < gMin {
			gMin = d.TempMin
		}
		if d.TempMax > gMax {
			gMax = d.TempMax
		}
	}
	span := gMax - gMin
	if span < 1 {
		span = 1
	}

	fmt.Printf("%s  %-10s %5s %5s  %-12s %6s %6s  %-11s %5s %3s  %s%s\n",
		b, "Day", "Hi", "Lo", "Temp range", "Rain", "Rain%", "Wind", "Gust", "UV", "Sky", rst)
	for _, d := range daily {
		hi := fmt.Sprintf("%d°", int(round(d.TempMax)))
		lo := fmt.Sprintf("%d°", int(round(d.TempMin)))
		bar := tempRangeBar(d.TempMin, d.TempMax, gMin, span, 12)
		rain := formatPrecip(d.PrecipSum)
		if rain == "" {
			rain = "·"
		}
		pct := "—"
		if d.PrecipProbMax > 0 {
			pct = fmt.Sprintf("%d", d.PrecipProbMax)
		}
		kmh := int(round(d.WindMax))
		windPlain := fmt.Sprintf("%s %2d km/h", windArrowFor(int(round(d.WindDirDominant))), kmh)
		gust := fmt.Sprintf("%d", int(round(d.GustMax)))
		uvVal := int(round(d.UVMax))
		sky := conditionHumanLabel(d.Condition)

		windCell := wrap(fmt.Sprintf("%-11s", windPlain), windColor(kmh))
		uvCell := wrap(fmt.Sprintf("%3d", uvVal), uvColor(uvVal))
		fmt.Printf("  %-10s %5s %5s  %s %6s %6s  %s %5s  %s  %s\n",
			d.Date.Format("Mon 2 Jan"), hi, lo, bar, rain, pct, windCell, gust, uvCell, sky)
	}
}

// tempRangeBar renders a width-w ASCII bar with this day's min→max segment
// positioned within the [gMin, gMin+span] range — the terminal equivalent of
// the web table's coloured range bars.
func tempRangeBar(minT, maxT, gMin, span float64, w int) string {
	left := int((minT - gMin) / span * float64(w))
	barLen := int((maxT-minT)/span*float64(w) + 0.5)
	if barLen < 1 {
		barLen = 1
	}
	if left < 0 {
		left = 0
	}
	if left > w-1 {
		left = w - 1
	}
	if left+barLen > w {
		barLen = w - left
	}
	right := w - left - barLen
	if right < 0 {
		right = 0
	}
	return strings.Repeat("░", left) +
		termplt.ColorYellow + strings.Repeat("█", barLen) + termplt.ColorReset +
		strings.Repeat("░", right)
}
