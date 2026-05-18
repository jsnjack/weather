/*
Copyright © 2025 YAUHEN SHULITSKI
*/
package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/jsnjack/termplt"
	"github.com/spf13/cobra"
)

var (
	FlagVersion     bool
	FlagStrLocation string
	FlagLat         float64
	FlagLon         float64
	FlagDebug       bool
	FlagTrace       bool
)

// Version is set at build time via ldflags; defaults to "dev".
var Version = "dev"

// loggerCleanup closes the trace log file if one was opened. Set in
// PersistentPreRunE, invoked from Execute on shutdown.
var loggerCleanup = func() {}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use: "weather",
	Long: `Shows the weather using the Buinealarm API.
By default, it tries to guess your location based on your IP address.
User can also specify the location manually.`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		level, tracePath := "", ""
		switch {
		case FlagTrace:
			level, tracePath = "trace", "/tmp/weather.log"
		case FlagDebug:
			level = "debug"
		}
		loggerCleanup = initLogger(tracePath, level)
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		if FlagVersion {
			fmt.Println(Version)
			return nil
		}

		loc, err := ResolveLocation()
		if err != nil {
			return fmt.Errorf("resolve location: %w", err)
		}

		prog := NewCLIProgress("rain forecast")
		glance, glanceErr := buildGlanceResponse(cmd.Context(), loc, prog)
		prog.Finish()
		if glanceErr != nil && glance == nil {
			return fmt.Errorf("forecast: %w", glanceErr)
		}

		fmt.Printf(termplt.ColorBold+"Weather in %s\n"+termplt.ColorReset, loc.Description)
		buinealarmForecast := glance.Buienalarm
		buineradarForecast := glance.Buineradar
		var desc string
		if buinealarmForecast != nil {
			desc = buinealarmForecast.Desc
		}
		printConditionLine(glance, desc)

		// Chart first — mirrors the Android widget layout, where the chart
		// fills the top and the per-corner stats sit beneath it.
		// termplt's chart string already ends in a blank line, so we don't
		// add one before the stats; in the dry branch we add one ourselves.
		if !glance.IsDry() {
			renderRainChart(buinealarmForecast, buineradarForecast)
		} else {
			fmt.Println()
		}

		printGlanceStats(glance)
		return nil
	},
}

func renderRainChart(alarm, radar *Forecast) {
	fmt.Printf("%sBuienalarm%s · %sBuineradar%s\n",
		termplt.ColorCyan, termplt.ColorReset,
		termplt.ColorPurple, termplt.ColorReset)
	chart := termplt.NewLineChart()
	var unit string
	if alarm != nil && len(alarm.Data) > 0 {
		ax, ay := make([]float64, 0, len(alarm.Data)), make([]float64, 0, len(alarm.Data))
		for _, p := range alarm.Data {
			ax = append(ax, float64(p.Time.Unix()))
			ay = append(ay, p.Value)
		}
		chart.AddLine(ax, ay, termplt.ColorCyan)
		unit = alarm.Type.Unit()
	}
	if radar != nil && len(radar.Data) > 0 {
		rx, ry := make([]float64, 0, len(radar.Data)), make([]float64, 0, len(radar.Data))
		for _, p := range radar.Data {
			// Buineradar runs past Buienalarm's horizon — cap so both lines
			// share the same x range.
			if alarm != nil && len(alarm.Data) > 0 {
				if p.Time.After(alarm.Data[len(alarm.Data)-1].Time) {
					break
				}
			}
			rx = append(rx, float64(p.Time.Unix()))
			ry = append(ry, p.Value)
		}
		chart.AddLine(rx, ry, termplt.ColorPurple)
		if unit == "" {
			unit = radar.Type.Unit()
		}
	}
	chart.SetXLabelAsTime("", "15:04")
	chart.SetYLabel(unit)
	fmt.Print(chart.String())
}

// printConditionLine prints the headline "Overcast — In 5 minutes, it will
// dry up" line. Kept separate from printGlanceStats so the caller can slot
// the chart between condition and stats.
func printConditionLine(g *glanceAPIResponse, desc string) {
	if g == nil {
		return
	}
	headline := conditionHumanLabel(g.Condition)
	if desc != "" {
		headline = headline + " — " + desc
	}
	fmt.Println(headline)
}

// printGlanceStats prints two compact lines mirroring the Android widget's
// corner clusters — one for "now" and one for "+2h":
//
//	now  20°  feels 20°  ↖  7 km/h  UV 0
//	+2h  17°  feels 17°  ↖ 12 km/h  UV 0
//	↓ sunset 21:10
//
// Wind/UV pick up caution/critical colour at the shared thresholds. Wind
// speeds are right-aligned to two digits so the arrows line up between
// rows. Sun events fall on their own line below the stats.
//
// No rain probability — Buinealarm's nowcast already gives an exact
// minute-by-minute picture across the 2h window.
func printGlanceStats(g *glanceAPIResponse) {
	if g == nil {
		return
	}
	fmt.Println(formatGlanceLine("now", g.Temperature.Now, g.FeelsLike.Now,
		g.Wind.Now.DirectionDeg, g.Wind.Now.SpeedKmh, g.UVIndex.Now))
	fmt.Println(formatGlanceLine("+2h", g.Temperature.End, g.FeelsLike.End,
		g.Wind.End.DirectionDeg, g.Wind.End.SpeedKmh, g.UVIndex.End))

	for _, ev := range g.Sun {
		t, err := time.Parse(time.RFC3339, ev.Time)
		if err != nil {
			continue
		}
		glyph := "↑"
		kind := "sunrise"
		if ev.Kind == "sunset" {
			glyph = "↓"
			kind = "sunset"
		}
		fmt.Printf("  %s%s %s %s%s\n",
			termplt.ColorYellow, glyph, kind, t.In(time.Local).Format("15:04"),
			termplt.ColorReset)
	}
}

func formatGlanceLine(label string, temp, feels, windDeg, windKmh, uv int) string {
	// %2d on the wind speed keeps the arrow column aligned between the
	// now/+2h rows even when speeds straddle single/double digits.
	wind := fmt.Sprintf("%s %2d km/h", windArrowFor(windDeg), windKmh)
	return fmt.Sprintf("  %s%s%s  %s%d°%s  feels %d°  %s  %s",
		termplt.ColorBold, label, termplt.ColorReset,
		termplt.ColorBold, temp, termplt.ColorReset,
		feels,
		wrap(wind, windColor(windKmh)),
		wrap(fmt.Sprintf("UV %d", uv), uvColor(uv)),
	)
}

// wrap wraps `text` in an ANSI colour code and a reset. Returns text
// unchanged when colour is empty so we don't emit stray reset codes.
func wrap(text, color string) string {
	if color == "" {
		return text
	}
	return color + text + termplt.ColorReset
}

func windColor(kmh int) string {
	switch {
	case kmh >= WindCriticalKmh:
		return termplt.ColorRed
	case kmh >= WindCautionKmh:
		return termplt.ColorYellow
	default:
		return ""
	}
}

func uvColor(uv int) string {
	switch {
	case uv >= UVCritical:
		return termplt.ColorRed
	case uv >= UVCaution:
		return termplt.ColorYellow
	default:
		return ""
	}
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	loggerCleanup()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&FlagVersion, "version", false, "print version and exit")
	rootCmd.PersistentFlags().BoolVarP(&FlagDebug, "debug", "d", false, "Debug-level logging on stderr.")
	rootCmd.PersistentFlags().BoolVar(&FlagTrace, "trace", false, "Trace-level logs to /tmp/weather.log (truncated each run).")
	rootCmd.PersistentFlags().Float64VarP(&FlagLat, "lat", "a", 0, "latitude")
	rootCmd.PersistentFlags().Float64VarP(&FlagLon, "lon", "o", 0, "longitude")
	rootCmd.PersistentFlags().StringVarP(&FlagStrLocation, "name", "n", "", "location name, e.g. 'Amsterdam'")
}
