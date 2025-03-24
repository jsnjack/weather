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
			desc, err := GetDescriptionFromCoordinates(FlagLat, FlagLon)
			if err != nil {
				DebugLogger.Printf("Error getting description from coordinates: %s\n", err)
				desc = fmt.Sprintf("Lat %.2f, Lon %.2f", FlagLat, FlagLon)
			}
			loc = Location{
				Latitude:    FlagLat,
				Longitude:   FlagLon,
				Description: desc,
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

		buinealarmForecast, err := GetBuinealarmForecast(loc.Latitude, loc.Longitude)
		if err != nil {
			return err
		}
		buineradarForecast, err := GetBuineradarForecast(loc.Latitude, loc.Longitude)
		if err != nil {
			return err
		}
		fmt.Printf(termplt.ColorBold+"Weather in %s\n"+termplt.ColorReset, loc.Description)
		fmt.Println("Next 2 hours (" + termplt.ColorCyan + "Buienalarm" + termplt.ColorReset + ", " + termplt.ColorPurple + "Buineradar" + termplt.ColorReset + ")")
		if buinealarmForecast.Desc != "" {
			fmt.Println(buinealarmForecast.Desc)
		}

		chart := termplt.NewLineChart()

		buinealarmY := []float64{}
		buinealarmX := []float64{}
		for _, point := range buinealarmForecast.Data {
			buinealarmY = append(buinealarmY, point.Precipitation)
			buinealarmX = append(buinealarmX, float64(point.Time.Unix()))
		}
		chart.AddLine(buinealarmX, buinealarmY, termplt.ColorCyan)

		buineradarY := []float64{}
		buineradarX := []float64{}
		for _, point := range buineradarForecast.Data {
			// Buineradar provides lomnger forecast, lets cut it off
			if point.Time.After(buinealarmForecast.Data[len(buinealarmForecast.Data)-1].Time) {
				break
			}
			buineradarY = append(buineradarY, point.Precipitation)
			buineradarX = append(buineradarX, float64(point.Time.Unix()))
		}
		chart.AddLine(buineradarX, buineradarY, termplt.ColorPurple)

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
