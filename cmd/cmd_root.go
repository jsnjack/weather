/*
Copyright © 2025 YAUHEN SHULITSKI
*/
package cmd

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/jsnjack/termplt"
	"github.com/spf13/cobra"
)

var FlagVersion bool
var FlagStrLocation string
var FlagLat float64
var FlagLon float64
var FlagDebug bool

var Version string

var Logger *log.Logger
var DebugLogger *log.Logger

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use: "weather",
	Long: `Shows the weather using the Buinealarm API.
By default, it tries to guess your location based on your IP address.
User can also specify the location manually.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		Logger = log.New(os.Stdout, "", log.Lmicroseconds|log.Lshortfile)

		if FlagDebug {
			DebugLogger = log.New(os.Stdout, "", log.Lmicroseconds|log.Lshortfile)
		} else {
			DebugLogger = log.New(io.Discard, "", 0)
		}

		if FlagVersion {
			fmt.Println(Version)
			return nil
		}

		var loc Location
		var err error

		if FlagLat != 0 || FlagLon != 0 {
			loc = Location{
				Latitude:    FlagLat,
				Longitude:   FlagLon,
				Description: fmt.Sprintf("Lat %.2f, Lon %.2f", FlagLat, FlagLon),
			}
		} else if FlagStrLocation != "" {
			loc, err = GetLocationFromString(FlagStrLocation)
			if err != nil {
				return err
			}
		} else {
			loc, err = GetLocationFromIP()
			if err != nil {
				return err
			}
		}

		forecast, err := GetForecast(loc.Latitude, loc.Longitude)
		if err != nil {
			return err
		}
		fmt.Printf("Weather in %s: %d°C\n", loc.Description, forecast.Temperature)
		fmt.Println(forecast.RainString())
		chart := termplt.NewLineChart()
		percY := []float64{}
		timeX := []float64{}
		for _, point := range forecast.Data {
			percY = append(percY, point.Precipitation)
			timeX = append(timeX, float64(point.Time.Unix()))
		}
		chart.AddLine(timeX, percY, termplt.ColorBlue)
		chart.SetXLabelAsTime("", "15:04")
		chart.SetYLabel("mm")
		fmt.Printf("%s", chart.String())
		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&FlagVersion, "version", "v", false, "print version and exit")
	rootCmd.PersistentFlags().BoolVarP(&FlagDebug, "debug", "d", false, "print debug information")
	rootCmd.PersistentFlags().Float64VarP(&FlagLat, "lat", "a", 0, "latitude")
	rootCmd.PersistentFlags().Float64VarP(&FlagLon, "lon", "o", 0, "longitude")
	rootCmd.PersistentFlags().StringVarP(&FlagStrLocation, "name", "n", "", "location name, e.g. 'Amsterdam'")
}
