/*
Copyright © 2025 YAUHEN SHULITSKI
*/
package cmd

import (
	"fmt"
	"os"

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
		buinealarmForecast, buineradarForecast, alarmErr, radarErr := fetchRain(cmd.Context(), loc.Latitude, loc.Longitude, prog)
		prog.Finish()
		if alarmErr != nil {
			return fmt.Errorf("buienalarm: %w", alarmErr)
		}
		if radarErr != nil {
			return fmt.Errorf("buineradar: %w", radarErr)
		}
		fmt.Printf(termplt.ColorBold+"Weather in %s\n"+termplt.ColorReset, loc.Description)
		fmt.Println("Next 2 hours (" + termplt.ColorCyan + "Buienalarm" + termplt.ColorReset + ", " + termplt.ColorPurple + "Buineradar" + termplt.ColorReset + ")")
		if buinealarmForecast.Desc != "" {
			fmt.Println(buinealarmForecast.Desc)
		}

		// We plot both Buinealarm and Buineradar forecasts on the same chart
		chart := termplt.NewLineChart()

		if len(buinealarmForecast.Data) > 0 {
			buinealarmY := []float64{}
			buinealarmX := []float64{}
			for _, point := range buinealarmForecast.Data {
				buinealarmY = append(buinealarmY, point.Value)
				buinealarmX = append(buinealarmX, float64(point.Time.Unix()))
			}
			chart.AddLine(buinealarmX, buinealarmY, termplt.ColorCyan)
		}

		if len(buineradarForecast.Data) > 0 {
			buineradarY := []float64{}
			buineradarX := []float64{}
			for _, point := range buineradarForecast.Data {
				// Buineradar provides longer forecast, lets cut it off
				if len(buinealarmForecast.Data) > 0 {
					if point.Time.After(buinealarmForecast.Data[len(buinealarmForecast.Data)-1].Time) {
						break
					}
				}
				buineradarY = append(buineradarY, point.Value)
				buineradarX = append(buineradarX, float64(point.Time.Unix()))
			}
			chart.AddLine(buineradarX, buineradarY, termplt.ColorPurple)
		}

		chart.SetXLabelAsTime("", "15:04")
		chart.SetYLabel(buinealarmForecast.Type.Unit())
		fmt.Printf("%s", chart.String())
		return nil
	},
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
