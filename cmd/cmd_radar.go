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
	"slices"
	"time"

	"github.com/spf13/cobra"
)

var radarOutput string
var radarAnimate bool
var radarDark bool
var radarRadius int
var radarSize int
var radarOutline bool

var radarCmd = &cobra.Command{
	Use:   "radar",
	Short: "Save local rain radar as a PNG or a two-hour animated GIF",
	Args:  cobra.NoArgs,
	RunE: func(command *cobra.Command, args []string) error {
		command.SilenceUsage = true
		if radarOutput == "" {
			return fmt.Errorf("--output must specify a destination file")
		}
		if !slices.Contains(localRadarRadiiKM, radarRadius) {
			return fmt.Errorf("--radius must be 25, 50 or 100")
		}
		if radarSize != localRadarSize && radarSize != localRadarLargeSize {
			return fmt.Errorf("--size must be %d or %d", localRadarSize, localRadarLargeSize)
		}
		if radarAnimate && (radarSize != localRadarSize || radarOutline) {
			return fmt.Errorf("--size and --outline apply only to the PNG export, not to --animate")
		}
		loc, err := ResolveLocation()
		if err != nil {
			return fmt.Errorf("resolve radar location: %w", err)
		}
		if radarAnimate {
			timeline, err := getLocalRadarTimelineRadius(command.Context(), loc.Latitude, loc.Longitude, radarRadius, radarDark)
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
			if _, err := fmt.Fprintf(command.OutOrStdout(), "Saved %s · %s to %s\n", radarOutput, timeline.Times[0].In(nowcastZone(loc.Latitude, loc.Longitude)).Format("15:04 MST"), timeline.Times[len(timeline.Times)-1].In(nowcastZone(loc.Latitude, loc.Longitude)).Format("15:04 MST")); err != nil {
				return fmt.Errorf("write radar result: %w", err)
			}
			return nil
		}
		radar, err := loadLocalRadarMap(command.Context(), loc.Latitude, loc.Longitude, localRadarOptions{radiusKM: radarRadius, size: radarSize, dark: radarDark, outline: radarOutline})
		if err != nil {
			return fmt.Errorf("load local radar: %w", err)
		}
		if radar == nil {
			return fmt.Errorf("no fresh nearby radar rain is available")
		}
		if err := os.WriteFile(radarOutput, radar.body, 0644); err != nil {
			return fmt.Errorf("save radar image: %w", err)
		}
		outline := ""
		if minutes := int(localRadarOutlineLead / time.Minute); radarOutline && radar.outlined {
			outline = fmt.Sprintf(" · outlined rain in %d minutes", minutes)
		} else if radarOutline {
			outline = fmt.Sprintf(" · no %d-minute outline: the forecast is dry or unavailable", minutes)
		}
		if _, err := fmt.Fprintf(command.OutOrStdout(), "Saved %s · observed %s%s\n", radarOutput, radar.observed.In(nowcastZone(loc.Latitude, loc.Longitude)).Format("15:04 MST"), outline); err != nil {
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
	radarCmd.Flags().BoolVar(&radarAnimate, "animate", false, "save a GIF from the current observation through the next two hours, playing once")
	radarCmd.Flags().IntVar(&radarRadius, "radius", int(localRadarRadiusM/1000), "map radius in km: 25, 50 or 100; other than 50, dry maps are saved too")
	radarCmd.Flags().BoolVar(&radarDark, "dark", false, "use a dark basemap while keeping rain colours unchanged")
	radarCmd.Flags().IntVar(&radarSize, "size", localRadarSize, "PNG width and height in pixels: 256 or 512 (the same area, sharper); PNG only, not with --animate")
	radarCmd.Flags().BoolVar(&radarOutline, "outline", false, "outline where KNMI's nowcast expects rain 30 minutes after the observation; PNG only, not with --animate")
	if err := radarCmd.MarkFlagRequired("output"); err != nil {
		panic(err)
	}
	rootCmd.AddCommand(radarCmd)
}
