package cmd

import (
	"bytes"
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/png"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var radarOutput string
var radarAnimate bool
var radarDark bool

var radarCmd = &cobra.Command{
	Use:   "radar",
	Short: "Save local rain radar as a PNG or a one-hour animated GIF",
	Args:  cobra.NoArgs,
	RunE: func(command *cobra.Command, args []string) error {
		command.SilenceUsage = true
		if radarOutput == "" {
			return fmt.Errorf("--output must specify a destination file")
		}
		loc, err := ResolveLocation()
		if err != nil {
			return fmt.Errorf("resolve radar location: %w", err)
		}
		if radarAnimate {
			timeline, err := getLocalRadarTimelineTheme(command.Context(), loc.Latitude, loc.Longitude, radarDark)
			if err != nil {
				return fmt.Errorf("load radar animation: %w", err)
			}
			if timeline == nil {
				return fmt.Errorf("no fresh nearby radar rain is available")
			}
			body, err := encodeLocalRadarGIF(timeline)
			if err != nil {
				return err
			}
			if err := os.WriteFile(radarOutput, body, 0644); err != nil {
				return fmt.Errorf("save radar animation: %w", err)
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Saved %s · %s to %s\n", radarOutput, timeline.Times[0].Format("15:04 MST"), timeline.Times[len(timeline.Times)-1].Format("15:04 MST")); err != nil {
				return fmt.Errorf("write radar result: %w", err)
			}
			return nil
		}
		radar, err := getLocalRadar(command.Context(), loc.Latitude, loc.Longitude)
		if err != nil {
			return fmt.Errorf("load local radar: %w", err)
		}
		if radar == nil || !freshRadarObservation(radar.observed, time.Now()) || !hasLocalRadarRain(radar.overlay) {
			return fmt.Errorf("no fresh nearby radar rain is available")
		}
		body, err := renderLocalRadarTheme(radar, loc.Latitude, loc.Longitude, radarDark)
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

func encodeLocalRadarGIF(timeline *localRadarTimeline) ([]byte, error) {
	atlas, err := png.Decode(bytes.NewReader(timeline.atlas))
	if err != nil {
		return nil, fmt.Errorf("decode radar animation atlas: %w", err)
	}
	animation := &gif.GIF{LoopCount: -1}
	for i, delay := range timeline.Delays {
		frame := image.NewPaletted(image.Rect(0, 0, timeline.Width, timeline.Height), palette.Plan9)
		origin := image.Pt(i%timeline.Columns*timeline.Width, i/timeline.Columns*timeline.Height)
		draw.FloydSteinberg.Draw(frame, frame.Bounds(), atlas, origin)
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, delay/10)
	}
	var encoded bytes.Buffer
	if err := gif.EncodeAll(&encoded, animation); err != nil {
		return nil, fmt.Errorf("encode radar animation: %w", err)
	}
	return encoded.Bytes(), nil
}

func init() {
	radarCmd.Flags().StringVar(&radarOutput, "output", "", "destination PNG or animated GIF file")
	radarCmd.Flags().BoolVar(&radarAnimate, "animate", false, "save a GIF from the current observation through the next hour, playing once")
	radarCmd.Flags().BoolVar(&radarDark, "dark", false, "use a dark basemap while keeping rain colours unchanged")
	if err := radarCmd.MarkFlagRequired("output"); err != nil {
		panic(err)
	}
	rootCmd.AddCommand(radarCmd)
}
