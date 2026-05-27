package cmd

import (
	"fmt"
	"time"

	"github.com/jsnjack/termplt"
	"github.com/spf13/cobra"
)

var FlagHourlyHours int

var hourlyCmd = &cobra.Command{
	Use:   "hourly",
	Short: "Hour-by-hour forecast for the next day (temp, rain, wind, UV)",
	Long: `hourly shows the next day hour by hour — temperature and feels-like,
precipitation, wind, and UV index. Mirrors the /hourly web page so the CLI and
browser stay in sync.`,
	RunE: runHourly,
}

func init() {
	rootCmd.AddCommand(hourlyCmd)
	hourlyCmd.Flags().IntVar(&FlagHourlyHours, "hours", 24, "forecast window in hours (6–48)")
}

func runHourly(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	hours := FlagHourlyHours
	if hours < 6 || hours > 48 {
		return fmt.Errorf("--hours must be between 6 and 48")
	}

	loc, err := ResolveLocation()
	if err != nil {
		return err
	}
	now := time.Now()
	start := now.Truncate(time.Hour)
	end := start.Add(time.Duration(hours) * time.Hour)

	prog := NewCLIProgress("hourly forecast")
	prog.AddTotal(1)
	// Fetch a little past the window so the last hour is covered across the
	// local-midnight boundary. Shares the cached Open-Meteo layer with /hourly.
	data, err := GetOpenMeteoRange(loc.Latitude, loc.Longitude, now, end.Add(2*time.Hour))
	prog.Inc(1)
	prog.Finish()
	if err != nil || data == nil || len(data.Hourly) == 0 {
		return fmt.Errorf("hourly forecast: %w", err)
	}

	rows := make([]HourlyForecast, 0, hours)
	for _, h := range data.Hourly {
		if h.Time.Before(start) || h.Time.After(end) {
			continue
		}
		rows = append(rows, h)
	}
	if len(rows) == 0 {
		return fmt.Errorf("hourly forecast: no data in the requested window")
	}

	fmt.Printf(termplt.ColorBold+"Hourly forecast for %s"+termplt.ColorReset+
		"  ·  %s → %s\n\n",
		loc.Description, start.Format("Mon 15:04"), end.Format("Mon 15:04"))

	renderHourlyTempChart(rows)
	fmt.Println()
	renderHourlyPrecipChart(rows)
	fmt.Println()
	renderHourlyTable(rows)
	return nil
}

func renderHourlyTempChart(rows []HourlyForecast) {
	fmt.Printf("%sTemp%s · %sFeels like%s (°C)\n",
		termplt.ColorYellow, termplt.ColorReset, termplt.ColorCyan, termplt.ColorReset)
	chart := termplt.NewLineChart()
	x := make([]float64, len(rows))
	temp := make([]float64, len(rows))
	feels := make([]float64, len(rows))
	for i, h := range rows {
		x[i] = float64(h.Time.Unix())
		temp[i] = h.Temperature
		feels[i] = h.ApparentTemperature
	}
	chart.AddLine(x, temp, termplt.ColorYellow)
	chart.AddLine(x, feels, termplt.ColorCyan)
	chart.SetXLabelAsTime("", "Mon 15h")
	chart.SetYLabel("°C")
	fmt.Print(chart.String())
}

func renderHourlyPrecipChart(rows []HourlyForecast) {
	maxP := 0.0
	for _, h := range rows {
		if h.Precipitation > maxP {
			maxP = h.Precipitation
		}
	}
	if maxP < DryThresholdMmH {
		fmt.Printf("%sPrecipitation%s — none expected in the window.\n",
			termplt.ColorCyan, termplt.ColorReset)
		return
	}
	fmt.Printf("%sPrecipitation%s (mm/h)\n", termplt.ColorCyan, termplt.ColorReset)
	chart := termplt.NewLineChart()
	x := make([]float64, len(rows))
	precip := make([]float64, len(rows))
	for i, h := range rows {
		x[i] = float64(h.Time.Unix())
		precip[i] = h.Precipitation
	}
	chart.AddLine(x, precip, termplt.ColorCyan)
	chart.SetXLabelAsTime("", "Mon 15h")
	chart.SetYLabel("mm")
	fmt.Print(chart.String())
}

func renderHourlyTable(rows []HourlyForecast) {
	b, rst := termplt.ColorBold, termplt.ColorReset
	fmt.Printf("%s  %-10s %5s %6s %6s %6s  %-11s %3s  %s%s\n",
		b, "Time", "Temp", "Feels", "Rain", "Rain%", "Wind", "UV", "Sky", rst)

	lastDay := -1
	for _, h := range rows {
		day := h.Time.YearDay()
		label := h.Time.Format("15:04")
		if day != lastDay {
			label = h.Time.Format("Mon 15:04")
			if lastDay != -1 {
				fmt.Println() // blank line between calendar days
			}
		}
		lastDay = day

		temp := fmt.Sprintf("%d°", int(round(h.Temperature)))
		feels := fmt.Sprintf("%d°", int(round(h.ApparentTemperature)))
		rain := formatPrecip(h.Precipitation)
		if rain == "" {
			rain = "·"
		}
		pct := "—"
		if h.PrecipitationProbability > 0 {
			pct = fmt.Sprintf("%d", h.PrecipitationProbability)
		}
		kmh := int(round(h.WindSpeed))
		windPlain := fmt.Sprintf("%s %2d km/h", windArrowFor(int(round(h.WindDirection))), kmh)
		uvVal := int(round(h.UVIndex))
		sky := conditionHumanLabel(wmoCondition(h.WeatherCode))

		// Pad the plain text to width first, then wrap the padded cell in
		// colour, so ANSI escapes don't throw off column alignment.
		windCell := wrap(fmt.Sprintf("%-11s", windPlain), windColor(kmh))
		uvCell := wrap(fmt.Sprintf("%3d", uvVal), uvColor(uvVal))
		fmt.Printf("  %-10s %5s %6s %6s %6s  %s %s  %s\n",
			label, temp, feels, rain, pct, windCell, uvCell, sky)
	}
}
