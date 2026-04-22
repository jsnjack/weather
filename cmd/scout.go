package cmd

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jsnjack/termplt"
	"github.com/spf13/cobra"
)

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// visibleWidth returns the column count of s: runes minus ANSI escape
// sequences. Every printable rune we use is width 1.
func visibleWidth(s string) int {
	return utf8.RuneCountInString(ansiRegex.ReplaceAllString(s, ""))
}

func padRight(s string, width int) string {
	v := visibleWidth(s)
	if v >= width {
		return s
	}
	return s + strings.Repeat(" ", width-v)
}

var (
	FlagScoutDays             int
	FlagScoutKmPerDay         float64
	FlagScoutMinTemp          float64
	FlagScoutStartDate        string
	FlagScoutBeamWidth        int
	FlagScoutPivotPenalty     float64
	FlagScoutRoundTrip        bool
	FlagScoutRoundTripPenalty float64
	FlagScoutTopN             int
	FlagScoutHeatmap          bool
	FlagScoutHeatmapGrid      int
)

const (
	scoutNumDirections = 8
	scoutFetchWorkers  = 4
	tailHeadSwitchKmh  = 5.0
)

var scoutCmd = &cobra.Command{
	Use:   "scout",
	Short: "Find the best multi-leg backpacking trip for the forecast window",
	Long: `Scout searches over multi-leg trips from your starting point and reports
the top plans. Each day's bearing is chosen independently, so the recommended
trips can pivot (e.g. S → S → SE → E → E) to follow dry air or tailwind.
Rain during the daytime window (10:00–20:00) disqualifies a day.

Use --round-trip to bias the search toward plans that end near the starting
point (with paired bearings that close the loop).`,
	RunE: runScout,
}

func init() {
	rootCmd.AddCommand(scoutCmd)
	scoutCmd.Flags().IntVar(&FlagScoutDays, "days", 5, "trip length in days")
	scoutCmd.Flags().Float64Var(&FlagScoutKmPerDay, "km-per-day", 100, "daily distance in km")
	scoutCmd.Flags().Float64Var(&FlagScoutMinTemp, "min-temp", 15, "preferred minimum daytime max temperature (°C)")
	scoutCmd.Flags().StringVar(&FlagScoutStartDate, "start-date", "", "trip start date YYYY-MM-DD (default: today)")
	scoutCmd.Flags().IntVar(&FlagScoutBeamWidth, "beam-width", 16, "beam search width (higher = slower, more options)")
	scoutCmd.Flags().Float64Var(&FlagScoutPivotPenalty, "pivot-penalty", 3, "score penalty applied to each bearing change (day-to-day turn)")
	scoutCmd.Flags().BoolVar(&FlagScoutRoundTrip, "round-trip", false, "prefer trips that end near the starting point")
	scoutCmd.Flags().Float64Var(&FlagScoutRoundTripPenalty, "round-trip-penalty", 20, "score penalty per 100km between end and start (only with --round-trip)")
	scoutCmd.Flags().IntVar(&FlagScoutTopN, "top", 3, "how many trip plans to print")
	scoutCmd.Flags().BoolVar(&FlagScoutHeatmap, "heatmap", false, "render a spatial weather heatmap instead of trip plans — you trace your own route")
	scoutCmd.Flags().IntVar(&FlagScoutHeatmapGrid, "heatmap-grid", 21, "heatmap resolution (NxN, odd number so start sits on a cell)")
}

func runScout(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true

	if FlagScoutDays <= 0 {
		return fmt.Errorf("--days must be positive")
	}
	if FlagScoutKmPerDay <= 0 {
		return fmt.Errorf("--km-per-day must be positive")
	}
	if FlagScoutBeamWidth <= 0 {
		return fmt.Errorf("--beam-width must be positive")
	}
	if FlagScoutTopN <= 0 {
		FlagScoutTopN = 1
	}

	startDate := time.Now()
	if FlagScoutStartDate != "" {
		parsed, err := time.Parse("2006-01-02", FlagScoutStartDate)
		if err != nil {
			return fmt.Errorf("invalid --start-date %q (want YYYY-MM-DD): %w", FlagScoutStartDate, err)
		}
		startDate = parsed
	}
	endDate := startDate.AddDate(0, 0, FlagScoutDays-1)

	loc, err := ResolveLocation()
	if err != nil {
		return err
	}

	cfg := beamConfig{
		KmPerDay:         FlagScoutKmPerDay,
		MinTemp:          FlagScoutMinTemp,
		BeamWidth:        FlagScoutBeamWidth,
		PivotPenalty:     FlagScoutPivotPenalty,
		RoundTrip:        FlagScoutRoundTrip,
		RoundTripPenalty: FlagScoutRoundTripPenalty,
	}

	fmt.Printf(termplt.ColorBold+"Scouting from %s"+termplt.ColorReset+
		"  ·  %d days × %.0f km/day  ·  %s → %s",
		loc.Description, FlagScoutDays, FlagScoutKmPerDay,
		startDate.Format("2006-01-02"), endDate.Format("2006-01-02"),
	)
	if FlagScoutRoundTrip {
		fmt.Print("  ·  round-trip")
	}
	if FlagScoutHeatmap {
		fmt.Print("  ·  heatmap")
	}
	fmt.Println()
	fmt.Println()

	if FlagScoutHeatmap {
		hm := RunHeatmap(loc.Latitude, loc.Longitude, startDate, FlagScoutDays, cfg)
		renderHeatmap(hm)
		return nil
	}

	trips := RunBeamSearch(loc.Latitude, loc.Longitude, startDate, FlagScoutDays, cfg)
	if len(trips) == 0 {
		fmt.Println(termplt.ColorRed + "No viable trip found — every bearing hit rain or severe gusts on at least one day." + termplt.ColorReset)
		return nil
	}

	topN := FlagScoutTopN
	if topN > len(trips) {
		topN = len(trips)
	}
	top := trips[:topN]

	labelsByTrip := annotateTripLabels(top)

	renderLegend()
	for i := range top {
		renderTrip(i+1, top[i], labelsByTrip[i], loc.Latitude, loc.Longitude, cfg)
		if i < len(top)-1 {
			fmt.Println()
		}
	}
	fmt.Println()
	renderRecommendation(top, labelsByTrip, cfg)
	return nil
}

// ---------- labels (reverse geocode) ----------

// annotateTripLabels reverse-geocodes the endpoint of each day for every trip,
// deduping by rounded lat/lon so overlapping trips share calls. Returns a
// slice of label-lists parallel to the trips slice (labels[i][d] = locality
// at the end of day d+1 of trip i).
func annotateTripLabels(trips []beamNode) [][]string {
	type geoKey struct{ LatR, LonR int }
	keyFor := func(lat, lon float64) geoKey {
		return geoKey{int(math.Round(lat * 100)), int(math.Round(lon * 100))}
	}

	type geoTask struct {
		Lat, Lon float64
		Key      geoKey
	}

	pending := map[geoKey]geoTask{}
	for _, t := range trips {
		for _, p := range t.Positions[1:] {
			k := keyFor(p.Lat, p.Lon)
			if _, seen := pending[k]; !seen {
				pending[k] = geoTask{Lat: p.Lat, Lon: p.Lon, Key: k}
			}
		}
	}

	resolved := make(map[geoKey]string, len(pending))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, scoutFetchWorkers)
	for _, t := range pending {
		wg.Add(1)
		sem <- struct{}{}
		go func(t geoTask) {
			defer wg.Done()
			defer func() { <-sem }()
			name, err := GetDescriptionFromCoordinates(t.Lat, t.Lon)
			if err != nil {
				DebugLogger.Printf("scout: reverse-geocode %.2f,%.2f: %s\n", t.Lat, t.Lon, err)
			}
			if name == "" {
				name = fmt.Sprintf("%.2f,%.2f", t.Lat, t.Lon)
			}
			mu.Lock()
			resolved[t.Key] = name
			mu.Unlock()
		}(t)
	}
	wg.Wait()

	out := make([][]string, len(trips))
	for i, t := range trips {
		out[i] = make([]string, 0, len(t.Bearings))
		for _, p := range t.Positions[1:] {
			out[i] = append(out[i], resolved[keyFor(p.Lat, p.Lon)])
		}
	}
	return out
}

// ---------- rendering ----------

const (
	cellWidth    = 9
	dayColWidth  = 5  // "Day5 "
	dirColWidth  = 4  // "NE ↗"
	tempColWidth = 5  // "21°"
	windColWidth = 5  // "T20"
)

func renderLegend() {
	g := termplt.ColorGreen
	r := termplt.ColorRed
	y := termplt.ColorYellow
	rst := termplt.ColorReset
	fmt.Println(termplt.ColorBold + "Legend:" + rst)
	fmt.Printf("  Each day row:  %sS  ↘%s  ~Eindhoven   %s19°%s  %sT8%s    — bearing, endpoint, daytime max temp, tail/head wind\n",
		"", "", g, rst, g, rst)
	fmt.Printf("  %sT<n>%s tailwind km/h (good)    %sH<n>%s headwind km/h (bad)    ·  mostly crosswind (<%.0f km/h along route)\n",
		g, rst, r, rst, tailHeadSwitchKmh)
	fmt.Printf("  %s*%s  below --min-temp (%.0f°C)    any rain or gust ≥60 km/h would disqualify a day\n",
		y, rst, FlagScoutMinTemp)
	fmt.Println()
}

// renderTrip prints a per-day breakdown of one trip plan, then a one-line summary.
func renderTrip(rank int, trip beamNode, labels []string, startLat, startLon float64, cfg beamConfig) {
	endLat, endLon := trip.Positions[len(trip.Positions)-1].Lat, trip.Positions[len(trip.Positions)-1].Lon
	endDist := HaversineKm(endLat, endLon, startLat, startLon)

	path := bearingPath(trip.Bearings)
	endLabel := "?"
	if len(labels) > 0 {
		endLabel = labels[len(labels)-1]
	}

	header := fmt.Sprintf("%sTrip %d%s   score %.0f   %s",
		termplt.ColorBold, rank, termplt.ColorReset, trip.Score, path,
	)
	if cfg.RoundTrip {
		header += fmt.Sprintf("   (ends %.0f km from start)", endDist)
	} else {
		header += fmt.Sprintf("   (ends ~%s, %.0f km away)", endLabel, endDist)
	}
	fmt.Println(header)

	pivots := countPivots(trip.Bearings)
	for i, b := range trip.Bearings {
		ds := trip.DailyScores[i]
		label := ""
		if i < len(labels) {
			label = labels[i]
		}
		renderDayRow(i+1, b, label, ds)
	}
	if pivots > 0 {
		fmt.Printf("  %s%d pivot%s%s\n", termplt.ColorPurple, pivots, pluralS(pivots), termplt.ColorReset)
	}
}

func renderDayRow(day int, bearingDeg float64, endLabel string, ds DayScore) {
	dayCol := padRight(fmt.Sprintf("Day%d", day), dayColWidth)
	dir := fmt.Sprintf("%-2s %s", CompassName(bearingDeg), CompassArrow(bearingDeg))
	dirCol := padRight(dir, dirColWidth)
	endCol := padRight("~"+endLabel, 22)

	tempCol := padRight(fmt.Sprintf("%.0f°", ds.MaxTemp), tempColWidth)
	tempColored := tempCol
	if ds.BelowMinTemp {
		tempColored = termplt.ColorYellow + strings.TrimRight(tempCol, " ") + "*" + termplt.ColorReset
		tempColored = padRight(tempColored, tempColWidth+1)
	} else {
		tempColored = termplt.ColorGreen + strings.TrimRight(tempCol, " ") + termplt.ColorReset
		tempColored = padRight(tempColored, tempColWidth+1)
	}

	var windLabel, windColor string
	mag := math.Abs(ds.TailwindAvg)
	switch {
	case ds.TailwindAvg > tailHeadSwitchKmh:
		windLabel = fmt.Sprintf("T%.0f", mag)
		windColor = termplt.ColorGreen
	case ds.TailwindAvg < -tailHeadSwitchKmh:
		windLabel = fmt.Sprintf("H%.0f", mag)
		windColor = termplt.ColorRed
	default:
		windLabel = "·"
		windColor = ""
	}
	windColored := windColor + windLabel + termplt.ColorReset
	windCol := padRight(windColored, windColWidth)

	fmt.Printf("  %s  %s  %s  %s  %s\n", dayCol, dirCol, endCol, tempColored, windCol)
}

// renderRecommendation prints one or two sentences pointing at the winner and
// what distinguishes it from the alternatives.
func renderRecommendation(trips []beamNode, labelsByTrip [][]string, cfg beamConfig) {
	if len(trips) == 0 {
		return
	}
	winner := trips[0]
	labels := labelsByTrip[0]

	endLabel := "the endpoint"
	if len(labels) > 0 {
		endLabel = "~" + labels[len(labels)-1]
	}

	minT, maxT := math.MaxFloat64, -math.MaxFloat64
	twSum, twCount := 0.0, 0
	pivots := countPivots(winner.Bearings)
	for _, ds := range winner.DailyScores {
		if ds.MaxTemp > maxT {
			maxT = ds.MaxTemp
		}
		if ds.MinTemp < minT {
			minT = ds.MinTemp
		}
		twSum += ds.TailwindAvg
		twCount++
	}
	twAvg := 0.0
	if twCount > 0 {
		twAvg = twSum / float64(twCount)
	}

	shape := "a straight bearing"
	if pivots == 1 {
		shape = "one pivot"
	} else if pivots > 1 {
		shape = fmt.Sprintf("%d pivots", pivots)
	}

	var wind string
	switch {
	case twAvg > tailHeadSwitchKmh:
		wind = fmt.Sprintf("avg tailwind %+.0f km/h", twAvg)
	case twAvg < -tailHeadSwitchKmh:
		wind = fmt.Sprintf("avg headwind %.0f km/h", math.Abs(twAvg))
	default:
		wind = "mostly crosswind"
	}

	fmt.Printf("%sRecommendation:%s Trip 1 — %s with %s, %.0f–%.0f°C, %s. Bearings: %s.\n",
		termplt.ColorBold, termplt.ColorReset,
		endLabel, shape, minT, maxT, wind, bearingPath(winner.Bearings),
	)
	if cfg.RoundTrip {
		endLat, endLon := winner.Positions[len(winner.Positions)-1].Lat, winner.Positions[len(winner.Positions)-1].Lon
		// Approx — caller already computed start coords in runScout, but we
		// don't hold them here; derive from Positions[0].
		start := winner.Positions[0]
		dist := HaversineKm(endLat, endLon, start.Lat, start.Lon)
		fmt.Printf("  Closes to %.0f km from start.\n", dist)
	}
}

// ---------- small helpers ----------

// bearingPath renders a sequence of bearings as "S → S → SE → E → E".
func bearingPath(bearings []float64) string {
	parts := make([]string, 0, len(bearings))
	for _, b := range bearings {
		parts = append(parts, CompassName(b))
	}
	return strings.Join(parts, " → ")
}

func countPivots(bearings []float64) int {
	pivots := 0
	for i := 1; i < len(bearings); i++ {
		if bearings[i] != bearings[i-1] {
			pivots++
		}
	}
	return pivots
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
