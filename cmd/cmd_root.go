/*
Copyright © 2025 YAUHEN SHULITSKI
*/
package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

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
		printGlanceSummary(glance, desc)

		if glance.IsDry() {
			// No useful rain chart in dry conditions — the hero line above
			// already tells the user "dry next 2h". Skip the chart noise.
			return nil
		}

		fmt.Println("Next 2 hours (" + termplt.ColorCyan + "Buienalarm" + termplt.ColorReset + ", " + termplt.ColorPurple + "Buineradar" + termplt.ColorReset + ")")
		// We plot both Buinealarm and Buineradar forecasts on the same chart
		chart := termplt.NewLineChart()
		var unit string

		if buinealarmForecast != nil && len(buinealarmForecast.Data) > 0 {
			buinealarmY := []float64{}
			buinealarmX := []float64{}
			for _, point := range buinealarmForecast.Data {
				buinealarmY = append(buinealarmY, point.Value)
				buinealarmX = append(buinealarmX, float64(point.Time.Unix()))
			}
			chart.AddLine(buinealarmX, buinealarmY, termplt.ColorCyan)
			unit = buinealarmForecast.Type.Unit()
		}

		if buineradarForecast != nil && len(buineradarForecast.Data) > 0 {
			buineradarY := []float64{}
			buineradarX := []float64{}
			for _, point := range buineradarForecast.Data {
				// Buineradar provides longer forecast, lets cut it off
				if buinealarmForecast != nil && len(buinealarmForecast.Data) > 0 {
					if point.Time.After(buinealarmForecast.Data[len(buinealarmForecast.Data)-1].Time) {
						break
					}
				}
				buineradarY = append(buineradarY, point.Value)
				buineradarX = append(buineradarX, float64(point.Time.Unix()))
			}
			chart.AddLine(buineradarX, buineradarY, termplt.ColorPurple)
			if unit == "" {
				unit = buineradarForecast.Type.Unit()
			}
		}

		chart.SetXLabelAsTime("", "15:04")
		chart.SetYLabel(unit)
		fmt.Printf("%s", chart.String())
		return nil
	},
}

// printGlanceSummary prints the now → +2h block as a small aligned table:
//
//	Overcast — It will stay dry for now
//	            now      +2h
//	  temp      13°      11°
//	  feels     11°      10°
//	  wind      → 11     → 6   km/h
//	  UV        1        0
//
//	  ↓ sunset 21:33
//
// Aligned columns are easier to scan than a single dense "·"-separated line.
// Wind/UV cells pick up caution/critical colour at the same thresholds as
// the web hero and the Android widget.
//
// No rain probability row — Buinealarm's nowcast already gives an exact
// minute-by-minute picture across the 2h window, so an Open-Meteo hourly
// probability over the same window is contradictory noise.
func printGlanceSummary(g *glanceAPIResponse, desc string) {
	if g == nil {
		return
	}
	headline := conditionHumanLabel(g.Condition)
	if desc != "" {
		headline = headline + " — " + desc
	}
	fmt.Println(headline)
	fmt.Println()

	const labelW = 6
	const colW = 9 // visible width of the "now" column; "+2h" extends to the end.
	fmt.Printf("  %s%s %s %s%s\n", termplt.ColorBold,
		padCol("", labelW), padCol("now", colW), "+2h", termplt.ColorReset)

	tempNow := fmt.Sprintf("%d°", g.Temperature.Now)
	tempEnd := fmt.Sprintf("%d°", g.Temperature.End)
	fmt.Printf("  %s %s %s\n", padCol("temp", labelW),
		wrap(padCol(tempNow, colW), termplt.ColorBold),
		wrap(tempEnd, termplt.ColorBold))

	feelsNow := fmt.Sprintf("%d°", g.FeelsLike.Now)
	feelsEnd := fmt.Sprintf("%d°", g.FeelsLike.End)
	fmt.Printf("  %s %s %s\n", padCol("feels", labelW),
		padCol(feelsNow, colW), feelsEnd)

	windNowTxt := fmt.Sprintf("%s %d", windArrowFor(g.Wind.Now.DirectionDeg), g.Wind.Now.SpeedKmh)
	windEndTxt := fmt.Sprintf("%s %d", windArrowFor(g.Wind.End.DirectionDeg), g.Wind.End.SpeedKmh)
	fmt.Printf("  %s %s %s km/h\n", padCol("wind", labelW),
		wrap(padCol(windNowTxt, colW), windColor(g.Wind.Now.SpeedKmh)),
		wrap(windEndTxt, windColor(g.Wind.End.SpeedKmh)))

	uvNow := fmt.Sprintf("%d", g.UVIndex.Now)
	uvEnd := fmt.Sprintf("%d", g.UVIndex.End)
	fmt.Printf("  %s %s %s\n", padCol("UV", labelW),
		wrap(padCol(uvNow, colW), uvColor(g.UVIndex.Now)),
		wrap(uvEnd, uvColor(g.UVIndex.End)))

	if len(g.Sun) > 0 {
		fmt.Println()
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
	fmt.Println()
}

// padCol right-pads `text` to `width` visible columns using rune count, so
// multi-byte runes like ° or compass arrows still align. Always returns
// plain text so colour wrapping can be applied separately.
func padCol(text string, width int) string {
	w := utf8.RuneCountInString(text)
	if w >= width {
		return text
	}
	return text + strings.Repeat(" ", width-w)
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
