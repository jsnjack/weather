package cmd

import (
	"fmt"
	"sort"
	"time"

	"github.com/jsnjack/termplt"
	"github.com/spf13/cobra"
)

const (
	outlookToday    = "today"
	outlookTomorrow = "tomorrow"
	outlookWeekend  = "weekend"
	rideWindowHours = 2
	rideWindowLimit = 3
)

type rideWindow struct {
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	RainChance int       `json:"rainChance"`
	Wind       float64   `json:"windKmh"`
	Gusts      float64   `json:"gustsKmh"`
	Feels      float64   `json:"feelsC"`
	UV         float64   `json:"uv"`
	score      float64
}

type detailedOutlook struct {
	Period string           `json:"period"`
	Start  time.Time        `json:"start"`
	End    time.Time        `json:"end"`
	Hours  []HourlyForecast `json:"hours"`
	Rides  []rideWindow     `json:"rideWindows"`
}

func init() {
	for _, period := range []string{outlookTomorrow, outlookWeekend} {
		rootCmd.AddCommand(&cobra.Command{
			Use:   period,
			Short: "Detailed " + period + " forecast and best dry ride windows",
			RunE:  func(command *cobra.Command, args []string) error { return runOutlook(command, period) },
		})
	}
	todayCmd.Flags().Bool("forecast", false, "show today's hourly forecast and ride windows instead of the heatmap")
}

func outlookBounds(period string, now time.Time) (time.Time, time.Time, error) {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	days := 1
	switch period {
	case outlookToday:
	case outlookTomorrow:
		start = start.AddDate(0, 0, 1)
	case outlookWeekend:
		offset := (int(time.Saturday) - int(now.Weekday()) + 7) % 7
		if now.Weekday() == time.Sunday {
			offset = 0
			days = 1
		} else {
			days = 2
		}
		start = start.AddDate(0, 0, offset)
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("period must be today, tomorrow or weekend")
	}
	return start, start.AddDate(0, 0, days), nil
}

func fetchDetailedOutlook(lat, lon float64, period string, now time.Time) (detailedOutlook, error) {
	// Relative dates are evaluated upstream in timezone=auto, including a first visit.
	url := fmt.Sprintf(openMeteoHourlyURL+"&forecast_days=7", lat, lon)
	body, err := openMeteoGetBody(url)
	if err != nil {
		return detailedOutlook{}, err
	}
	data, err := parseOpenMeteoRange(body, lat, lon)
	if err != nil {
		return detailedOutlook{}, err
	}
	return buildDetailedOutlook(data, period, now)
}

func buildDetailedOutlook(data *OpenMeteoData, period string, now time.Time) (detailedOutlook, error) {
	if data == nil || len(data.Hourly) == 0 {
		return detailedOutlook{}, fmt.Errorf("detailed forecast: no hourly data")
	}
	now = now.In(data.Hourly[0].Time.Location())
	start, end, err := outlookBounds(period, now)
	if err != nil {
		return detailedOutlook{}, err
	}
	result := detailedOutlook{Period: period, Start: start, End: end, Hours: []HourlyForecast{}, Rides: []rideWindow{}}
	for _, hour := range data.Hourly {
		if !hour.Time.Before(start) && hour.Time.Before(end) {
			result.Hours = append(result.Hours, hour)
		}
	}
	if len(result.Hours) == 0 {
		return detailedOutlook{}, fmt.Errorf("detailed forecast: no data for %s", period)
	}
	result.Rides = rankRideWindows(result.Hours, data.Daily, now)
	return result, nil
}

func rankRideWindows(hours []HourlyForecast, days []DailyForecast, now time.Time) []rideWindow {
	candidates := make([]rideWindow, 0)
	for i := 0; i+rideWindowHours <= len(hours); i++ {
		first := hours[i]
		if first.Time.Before(now) {
			continue
		}
		end := first.Time.Add(rideWindowHours * time.Hour)
		daylight := false
		for _, day := range days {
			if !first.Time.Before(day.Sunrise) && !end.After(day.Sunset) {
				daylight = true
				break
			}
		}
		if !daylight {
			continue
		}
		ride := rideWindow{Start: first.Time, End: end}
		valid := true
		for j := 0; j < rideWindowHours; j++ {
			hour := hours[i+j]
			if !hour.Time.Equal(first.Time.Add(time.Duration(j)*time.Hour)) || hour.Precipitation >= DryThresholdMmH || hour.WindSpeed >= WindCriticalKmh || hour.WindGusts >= WindCriticalKmh {
				valid = false
				break
			}
			ride.RainChance = max(ride.RainChance, hour.PrecipitationProbability)
			ride.Wind = max(ride.Wind, hour.WindSpeed)
			ride.Gusts = max(ride.Gusts, hour.WindGusts)
			ride.UV = max(ride.UV, hour.UVIndex)
			ride.Feels += hour.ApparentTemperature / rideWindowHours
		}
		if !valid {
			continue
		}
		comfort := ride.Feels - 18
		if comfort < 0 {
			comfort = -comfort
		}
		ride.score = float64(ride.RainChance) + ride.Wind + ride.Gusts/2 + comfort + ride.UV
		candidates = append(candidates, ride)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score < candidates[j].score })
	best := make([]rideWindow, 0, rideWindowLimit)
	for _, candidate := range candidates {
		overlaps := false
		for _, chosen := range best {
			if candidate.Start.Before(chosen.End) && candidate.End.After(chosen.Start) {
				overlaps = true
				break
			}
		}
		if !overlaps {
			best = append(best, candidate)
		}
		if len(best) == rideWindowLimit {
			break
		}
	}
	return best
}

func runOutlook(command *cobra.Command, period string) error {
	command.SilenceUsage = true
	location, err := ResolveLocation()
	if err != nil {
		return fmt.Errorf("resolve detailed forecast location: %w", err)
	}
	progress := NewCLIProgress(period + " forecast")
	progress.AddTotal(1)
	result, err := fetchDetailedOutlook(location.Latitude, location.Longitude, period, time.Now())
	progress.Inc(1)
	progress.Finish()
	if err != nil {
		return fmt.Errorf("%s forecast: %w", period, err)
	}
	fmt.Printf(termplt.ColorBold+"%s forecast for %s"+termplt.ColorReset+" · %s → %s\n\n", period, location.Description, result.Start.Format("Mon 2 Jan"), result.End.Add(-time.Nanosecond).Format("Mon 2 Jan"))
	renderHourlyTempChart(result.Hours)
	fmt.Println()
	renderHourlyPrecipChart(result.Hours)
	fmt.Println()
	renderHourlyTable(result.Hours)
	fmt.Println("\nBest dry 2-hour ride windows (daylight; gusts below 50 km/h):")
	if len(result.Rides) == 0 {
		fmt.Println("No suitable daylight window in the forecast.")
	}
	for _, ride := range result.Rides {
		fmt.Printf("  %s–%s · rain chance %d%% · wind %.0f / gusts %.0f km/h · feels %.0f°C · UV %.0f\n", ride.Start.Format("Mon 15:04"), ride.End.Format("15:04"), ride.RainChance, ride.Wind, ride.Gusts, ride.Feels, ride.UV)
	}
	return nil
}
