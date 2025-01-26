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

var FlagVersion bool
var Version string

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "weather",
	Short: "shows the weather using the Buinealarm API",
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		if FlagVersion {
			fmt.Println(Version)
			return nil
		}

		loc, _ := GetLocationFromIP()
		forecast, _ := GetForecast(loc.Latitude, loc.Longitude)
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
}
