package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var radarOutput string

var radarCmd = &cobra.Command{
	Use:   "radar",
	Short: "Save the latest local rain radar as a PNG",
	Args:  cobra.NoArgs,
	RunE: func(command *cobra.Command, args []string) error {
		command.SilenceUsage = true
		if radarOutput == "" {
			return fmt.Errorf("--output must specify a PNG file")
		}
		loc, err := ResolveLocation()
		if err != nil {
			return fmt.Errorf("resolve radar location: %w", err)
		}
		radar, err := getLocalRadar(command.Context(), loc.Latitude, loc.Longitude)
		if err != nil {
			return fmt.Errorf("load local radar: %w", err)
		}
		if radar == nil || !freshRadarObservation(radar.observed, time.Now()) || !hasLocalRadarRain(radar.overlay) {
			return fmt.Errorf("no fresh nearby radar rain is available")
		}
		body, err := renderLocalRadar(radar, loc.Latitude, loc.Longitude)
		if err != nil {
			return fmt.Errorf("render local radar: %w", err)
		}
		if err := os.WriteFile(radarOutput, body, 0644); err != nil {
			return fmt.Errorf("save radar image: %w", err)
		}
		if _, err := fmt.Fprintf(command.OutOrStdout(), "Saved %s · observed %s\n", radarOutput, radar.observed.Format("15:04 MST")); err != nil {
			return fmt.Errorf("write radar result: %w", err)
		}
		return nil
	},
}

func init() {
	radarCmd.Flags().StringVar(&radarOutput, "output", "", "destination PNG file")
	if err := radarCmd.MarkFlagRequired("output"); err != nil {
		panic(err)
	}
	rootCmd.AddCommand(radarCmd)
}
