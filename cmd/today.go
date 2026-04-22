package cmd

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/jsnjack/termplt"
	"github.com/spf13/cobra"
)

var (
	FlagTodayHours  int
	FlagTodayStart  string
	FlagTodayRadius float64
	FlagTodayGrid   int
)

var todayCmd = &cobra.Command{
	Use:   "today",
	Short: "Plan a short ride in the next few hours (rain-timing heatmap)",
	Long: `today renders a compact weather heatmap around your location covering the
next few hours. Each cell's background colour tells you when rain arrives
during your ride window; the symbol shows the wind direction and strength.
Useful for "it's 10am, I'm thinking about a ride tonight — where's dry?"`,
	RunE: runToday,
}

func init() {
	rootCmd.AddCommand(todayCmd)
	todayCmd.Flags().IntVar(&FlagTodayHours, "hours", 6, "ride window length in hours (1–24)")
	todayCmd.Flags().StringVar(&FlagTodayStart, "start", "", "ride start time HH:MM (default: now + 30 min rounded up)")
	todayCmd.Flags().Float64Var(&FlagTodayRadius, "radius", 50, "map radius in km")
	todayCmd.Flags().IntVar(&FlagTodayGrid, "grid", 21, "heatmap resolution (NxN; odd, clamped to ≥5)")
}

// todayCell is the scored result for one grid cell over the ride window.
type todayCell struct {
	DryHours      int     // consecutive dry hours from ride start (0..windowHours)
	WindBlowsTo   float64 // degrees, 0=N — where wind is pushing you (midpoint hour)
	WindSpeed     float64 // km/h, midpoint hour sustained wind
	Sea           bool
	NoData        bool
}

// hourlyWind holds a single hour's wind observation for the evolution strip.
type hourlyWind struct {
	BlowsTo float64 // degrees, 0 = N — where wind is pushing
	Speed   float64 // km/h, sustained
}

// sectorEvolution is the per-hour wind sequence for one compass sector,
// sampled at half-radius from the start.
type sectorEvolution struct {
	Name      string
	Wind      []hourlyWind // one entry per hour of the ride window
	OverWater bool
	NoData    bool
}

type todayResult struct {
	Cells       [][]todayCell // [row][col]; row 0 = north
	StepKm      float64
	StartLat    float64
	StartLon    float64
	StartTime   time.Time
	WindowHours int
	Sectors     []sectorEvolution // 8 entries in compass order: N, NE, E, SE, S, SW, W, NW
}

func runToday(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	if FlagTodayHours <= 0 || FlagTodayHours > 24 {
		return fmt.Errorf("--hours must be between 1 and 24")
	}
	if FlagTodayRadius <= 0 {
		return fmt.Errorf("--radius must be positive")
	}

	startTime, err := resolveTodayStart()
	if err != nil {
		return err
	}
	endTime := startTime.Add(time.Duration(FlagTodayHours) * time.Hour)

	loc, err := ResolveLocation()
	if err != nil {
		return err
	}

	fmt.Printf(termplt.ColorBold+"Today around %s"+termplt.ColorReset+
		"  ·  %s–%s  ·  %.0f km radius\n\n",
		loc.Description,
		startTime.Format("15:04"), endTime.Format("15:04"),
		FlagTodayRadius,
	)

	result := runTodayGrid(loc.Latitude, loc.Longitude, startTime, FlagTodayHours)
	renderToday(result)
	fmt.Println()
	renderWindEvolution(result)
	fmt.Println()
	renderTodayLegend()
	fmt.Println()
	printTodayRecommendation(result)
	return nil
}

// resolveTodayStart picks the ride start time from the --start flag or a
// "next full hour at least 30 min in the future" default.
func resolveTodayStart() (time.Time, error) {
	now := time.Now()
	if FlagTodayStart == "" {
		t := now.Add(30 * time.Minute)
		t = t.Truncate(time.Hour).Add(time.Hour)
		return t, nil
	}
	parsed, err := time.Parse("15:04", FlagTodayStart)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --start %q (want HH:MM): %w", FlagTodayStart, err)
	}
	return time.Date(now.Year(), now.Month(), now.Day(),
		parsed.Hour(), parsed.Minute(), 0, 0, now.Location()), nil
}

func todayGridSize() int {
	n := FlagTodayGrid
	if n < 5 {
		n = 5
	}
	if n%2 == 0 {
		n++
	}
	return n
}

// runTodayGrid builds the sample grid, fetches hourly weather for every cell
// across the ride window, and scores each cell.
func runTodayGrid(startLat, startLon float64, startTime time.Time, windowHours int) todayResult {
	gridSize := todayGridSize()
	halfSpanKm := FlagTodayRadius
	stepKm := halfSpanKm / float64(gridSize/2)

	latStep := stepKm / 111.0
	lonFactor := 111.0 * math.Cos(startLat*math.Pi/180)
	if lonFactor < 1 {
		lonFactor = 1
	}
	lonStep := stepKm / lonFactor

	type cellCoord struct {
		Lat, Lon float64
		Row, Col int
	}
	cells := make([]cellCoord, 0, gridSize*gridSize)
	for row := 0; row < gridSize; row++ {
		for col := 0; col < gridSize; col++ {
			northCells := float64(gridSize/2 - row)
			eastCells := float64(col - gridSize/2)
			cells = append(cells, cellCoord{
				Lat: startLat + northCells*latStep,
				Lon: startLon + eastCells*lonStep,
				Row: row,
				Col: col,
			})
		}
	}

	// Fetch boundary dates — the window might cross midnight.
	endTime := startTime.Add(time.Duration(windowHours) * time.Hour)
	startDate := time.Date(startTime.Year(), startTime.Month(), startTime.Day(),
		0, 0, 0, 0, startTime.Location())
	endDate := time.Date(endTime.Year(), endTime.Month(), endTime.Day(),
		0, 0, 0, 0, endTime.Location())

	type cellData struct {
		Row, Col int
		Data     *OpenMeteoData
		Err      error
	}
	results := make([]cellData, len(cells))
	sem := make(chan struct{}, scoutFetchWorkers)
	var wg sync.WaitGroup
	for i, c := range cells {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c cellCoord) {
			defer wg.Done()
			defer func() { <-sem }()
			data, err := GetOpenMeteoRange(c.Lat, c.Lon, startDate, endDate)
			results[i] = cellData{Row: c.Row, Col: c.Col, Data: data, Err: err}
		}(i, c)
	}
	wg.Wait()

	out := todayResult{
		StepKm:      stepKm,
		StartLat:    startLat,
		StartLon:    startLon,
		StartTime:   startTime,
		WindowHours: windowHours,
		Cells:       make([][]todayCell, gridSize),
	}
	for row := 0; row < gridSize; row++ {
		out.Cells[row] = make([]todayCell, gridSize)
	}

	// Which grid (row, col) positions are sector probes? Same 8 sampled in
	// printTodayRecommendation: N, NE, E, SE, S, SW, W, NW at half-radius.
	mid := gridSize / 2
	half := mid / 2
	if half < 1 {
		half = 1
	}
	sectorNames := []string{"N", "NE", "E", "SE", "S", "SW", "W", "NW"}
	sectorOffsets := [8][2]int{
		{-half, 0}, {-half, half}, {0, half}, {half, half},
		{half, 0}, {half, -half}, {0, -half}, {-half, -half},
	}
	sectorRC := map[[2]int]int{}
	for i, off := range sectorOffsets {
		sectorRC[[2]int{mid + off[0], mid + off[1]}] = i
	}
	out.Sectors = make([]sectorEvolution, 8)
	for i, name := range sectorNames {
		out.Sectors[i] = sectorEvolution{Name: name}
	}

	for _, rd := range results {
		if rd.Err != nil || rd.Data == nil || len(rd.Data.Hourly) == 0 {
			out.Cells[rd.Row][rd.Col] = todayCell{NoData: true}
			if si, ok := sectorRC[[2]int{rd.Row, rd.Col}]; ok {
				out.Sectors[si].NoData = true
			}
			continue
		}
		// Score sea cells too — the weather over water is meaningful for
		// reading fronts approaching from the sea. The Sea flag just marks
		// the cell visually and keeps it out of the "best direction" pick.
		cell := scoreRideCell(rd.Data.Hourly, startTime, windowHours)
		if rd.Data.IsSea() {
			cell.Sea = true
		}
		out.Cells[rd.Row][rd.Col] = cell

		if si, ok := sectorRC[[2]int{rd.Row, rd.Col}]; ok {
			out.Sectors[si].Wind = extractHourlyWind(rd.Data.Hourly, startTime, windowHours)
			if rd.Data.IsSea() {
				out.Sectors[si].OverWater = true
			}
		}
	}
	return out
}

// extractHourlyWind pulls per-hour wind for the ride window at one cell.
// BlowsTo is converted from Open-Meteo's meteorological "comes-from" convention.
func extractHourlyWind(hourly []HourlyForecast, startTime time.Time, windowHours int) []hourlyWind {
	const hourKey = "2006-01-02T15"
	byHour := make(map[string]HourlyForecast, len(hourly))
	for _, h := range hourly {
		byHour[h.Time.Format(hourKey)] = h
	}
	out := make([]hourlyWind, 0, windowHours)
	for i := 0; i < windowHours; i++ {
		target := startTime.Add(time.Duration(i) * time.Hour).Format(hourKey)
		if h, ok := byHour[target]; ok {
			out = append(out, hourlyWind{
				BlowsTo: math.Mod(h.WindDirection+180, 360),
				Speed:   h.WindSpeed,
			})
		} else {
			out = append(out, hourlyWind{})
		}
	}
	return out
}

// scoreRideCell walks the ride window hour-by-hour and counts consecutive dry
// hours from the start. Also records midpoint-hour wind direction + speed.
// Uses formatted "YYYY-MM-DDTHH" strings as keys so lookup is robust to
// time.Time Location differences (Open-Meteo returns local times per grid
// cell; user's startTime is in user local — same timezone for short radius).
func scoreRideCell(hourly []HourlyForecast, startTime time.Time, windowHours int) todayCell {
	const hourKey = "2006-01-02T15"
	byHour := make(map[string]HourlyForecast, len(hourly))
	for _, h := range hourly {
		byHour[h.Time.Format(hourKey)] = h
	}

	dryCount := 0
	for i := 0; i < windowHours; i++ {
		target := startTime.Add(time.Duration(i) * time.Hour).Format(hourKey)
		h, ok := byHour[target]
		if !ok {
			return todayCell{DryHours: dryCount, NoData: dryCount == 0}
		}
		if h.Precipitation > rainThresholdMm {
			break
		}
		dryCount++
	}

	cell := todayCell{DryHours: dryCount}

	mid := startTime.Add(time.Duration(windowHours/2) * time.Hour).Format(hourKey)
	if h, ok := byHour[mid]; ok {
		// Open-Meteo gives "wind direction" = where it's coming FROM.
		// Convert to "blows to" so arrows intuitively indicate push.
		cell.WindBlowsTo = math.Mod(h.WindDirection+180, 360)
		cell.WindSpeed = h.WindSpeed
	}
	return cell
}

// ---------- rendering ----------

func renderToday(r todayResult) {
	gridSize := todayGridSize()
	mid := gridSize / 2
	for row := 0; row < gridSize; row++ {
		fmt.Print("  ")
		for col := 0; col < gridSize; col++ {
			isStart := row == mid && col == mid
			fmt.Print(todayCellGlyph(r.Cells[row][col], r.WindowHours, isStart))
		}
		northCells := mid - row
		fmt.Printf("  %+4d km\n", int(float64(northCells)*r.StepKm))
	}
	// Bottom axis.
	fmt.Print("  ")
	eastEdge := int(float64(mid) * r.StepKm)
	left := fmt.Sprintf("-%d km W", eastEdge)
	right := fmt.Sprintf("+%d km E", eastEdge)
	const mark = "start"
	axisWidth := gridSize * 2
	midStart := (axisWidth - len(mark)) / 2
	leftGap := midStart - len(left)
	if leftGap < 1 {
		leftGap = 1
	}
	rightGap := axisWidth - midStart - len(mark) - len(right)
	if rightGap < 1 {
		rightGap = 1
	}
	fmt.Printf("%s%s%s%s%s\n", left, strings.Repeat(" ", leftGap), mark, strings.Repeat(" ", rightGap), right)
}

// todayCellGlyph returns the 2-char rendered cell.
//   - Background = rain-timing band.
//   - Char 1 = compass arrow (direction wind blows TO) or space if calm.
//   - Char 2 = strength marker (· ~ ≈) or calm mark.
//   - Sea / raining-now cells use an override glyph.
//   - Start cell overlays ● (wind info hidden — read neighbours).
func todayCellGlyph(c todayCell, windowHours int, isStart bool) string {
	rst := termplt.ColorReset
	// Start cell always shows the ● marker regardless of underlying data.
	if isStart {
		bg := todayBg(rainTimingBand(c.DryHours, windowHours))
		if c.NoData {
			bg = termplt.ColorBackgroundBrightBlack
		}
		return bg + termplt.ColorBold + termplt.ColorWhite + " ●" + rst
	}
	if c.NoData {
		return termplt.ColorBackgroundBrightBlack + "  " + rst
	}

	band := rainTimingBand(c.DryHours, windowHours)
	bg := todayBg(band)

	if band == 3 {
		// Raining now / within 1h.
		return bg + termplt.ColorBold + termplt.ColorWhite + " ✗" + rst
	}

	// Cyan foreground marks cells that are over water — weather still shown,
	// but you can't ride there. Land cells use the terminal's default fg.
	fg := ""
	if c.Sea {
		fg = termplt.ColorCyan
	}
	return bg + fg + todayWindGlyph(c) + rst
}

func todayBg(band int) string {
	switch band {
	case 0:
		return termplt.ColorBackgroundBrightGreen
	case 1:
		return termplt.ColorBackgroundGreen
	case 2:
		return termplt.ColorBackgroundYellow
	default:
		return termplt.ColorBackgroundRed
	}
}

// rainTimingBand maps (dryHours, windowHours) to a quality band:
// 0 = full window dry, 1 = rain in final 1–2h, 2 = rain in middle, 3 = rain now / within 1h.
func rainTimingBand(dryHours, windowHours int) int {
	switch {
	case dryHours >= windowHours:
		return 0
	case dryHours >= windowHours-2:
		return 1
	case dryHours >= 2:
		return 2
	default:
		return 3
	}
}

// todayWindGlyph returns the 2-char in-cell wind glyph: arrow + intensity marker.
// Calm winds get " ·" (no direction, just a centered dot).
func todayWindGlyph(c todayCell) string {
	band := WindBand(c.WindSpeed)
	if band == 0 {
		return " ·"
	}
	arrow := CompassArrow(c.WindBlowsTo)
	var marker string
	switch band {
	case 1:
		marker = "·"
	case 2:
		marker = "~"
	default:
		marker = "≈"
	}
	return arrow + marker
}

// renderWindEvolution prints a compact strip: one row per compass sector at
// mid-radius, one arrow per hour of the ride window. The arrow points where
// the wind pushes you at that hour; · means calm (≤10 km/h, direction
// meaningless). Lets you see at a glance whether your tailwind will hold.
func renderWindEvolution(r todayResult) {
	if len(r.Sectors) == 0 {
		return
	}
	gridSize := todayGridSize()
	half := (gridSize / 2) / 2
	if half < 1 {
		half = 1
	}
	sampleKm := float64(half) * r.StepKm

	b := termplt.ColorBold
	rst := termplt.ColorReset
	fmt.Printf("%sWind evolution at ~%.0f km out%s — arrow = where wind pushes you, %s·%s = calm:\n",
		b, sampleKm, rst, termplt.ColorCyan, rst)

	// Header row: "      HH  HH  HH  ..."  (6 chars for label area, 4 per hour).
	fmt.Print("        ")
	for i := 0; i < r.WindowHours; i++ {
		h := r.StartTime.Add(time.Duration(i) * time.Hour).Hour()
		fmt.Printf("%02d  ", h)
	}
	fmt.Println()

	for _, sec := range r.Sectors {
		fmt.Printf("  %-4s  ", sec.Name)
		if sec.NoData {
			fmt.Println("(no data)")
			continue
		}
		if sec.OverWater {
			// Still show evolution — weather over water is meaningful even if
			// the sector itself isn't rideable. Tint cyan so you remember.
			fmt.Print(termplt.ColorCyan)
		}
		for _, w := range sec.Wind {
			glyph := "·"
			if w.Speed > windCalmKmh {
				glyph = CompassArrow(w.BlowsTo)
			}
			fmt.Print(glyph + "   ")
		}
		if sec.OverWater {
			fmt.Print(rst + "  (water)")
		}
		fmt.Println()
	}
}

func renderTodayLegend() {
	rst := termplt.ColorReset
	b := termplt.ColorBold
	sw := func(bg, body string) string { return bg + body + rst }
	fmt.Println(b + "Legend" + rst + " — background = rain timing, symbol = wind:")
	fmt.Println()
	fmt.Println(b + "  Rain timing (cell background)" + rst)
	fmt.Printf("    %s    dry the entire %d-hour window\n", sw(termplt.ColorBackgroundBrightGreen, "   "), FlagTodayHours)
	fmt.Printf("    %s    rain in the final 1–2 h\n", sw(termplt.ColorBackgroundGreen, "   "))
	fmt.Printf("    %s    rain in the middle of the window\n", sw(termplt.ColorBackgroundYellow, "   "))
	fmt.Printf("    %s    raining now or within 1 h (shown as %s ✗ %s)\n",
		sw(termplt.ColorBackgroundRed, "   "),
		termplt.ColorBackgroundRed+b+termplt.ColorWhite, rst)
	fmt.Println()
	fmt.Println(b + "  Wind — arrow points where wind pushes you, marker is strength" + rst)
	fmt.Printf("    %s    calm, ≤%.0f km/h\n", sw(termplt.ColorBackgroundGreen, " · "), windCalmKmh)
	fmt.Printf("    %s    breezy, %.0f–%.0f km/h\n", sw(termplt.ColorBackgroundGreen, "→·"), windCalmKmh, windBreezyKmh)
	fmt.Printf("    %s    windy, %.0f–%.0f km/h\n", sw(termplt.ColorBackgroundGreen, "→~"), windBreezyKmh, windWindyKmh)
	fmt.Printf("    %s    strong, %.0f–%.0f km/h\n", sw(termplt.ColorBackgroundGreen, "→≈"), windWindyKmh, windStrongKmh)
	fmt.Println()
	fmt.Println(b + "  Overrides" + rst)
	fmt.Printf("    %s    over water (weather still shown; you can't ride there)\n",
		termplt.ColorBackgroundBrightGreen+termplt.ColorCyan+"→·"+rst)
	fmt.Printf("    %s    your starting point\n",
		termplt.ColorBackgroundBrightGreen+b+termplt.ColorWhite+" ●"+rst)
}

// printTodayRecommendation samples 8 compass sectors at half-radius and
// reports best + worst direction to head.
func printTodayRecommendation(r todayResult) {
	gridSize := todayGridSize()
	mid := gridSize / 2
	half := mid / 2
	if half < 1 {
		half = 1
	}

	type sector struct {
		Name     string
		Bearing  float64
		Cell     todayCell
	}
	// Offsets to grid cells at half-radius in each of 8 directions.
	sectors := []sector{
		{"N", 0, r.Cells[mid-half][mid]},
		{"NE", 45, r.Cells[mid-half][mid+half]},
		{"E", 90, r.Cells[mid][mid+half]},
		{"SE", 135, r.Cells[mid+half][mid+half]},
		{"S", 180, r.Cells[mid+half][mid]},
		{"SW", 225, r.Cells[mid+half][mid-half]},
		{"W", 270, r.Cells[mid][mid-half]},
		{"NW", 315, r.Cells[mid-half][mid-half]},
	}

	type scored struct {
		sector
		DryHours int
		Tailwind float64
	}
	rideable := make([]scored, 0, 8)
	for _, s := range sectors {
		if s.Cell.Sea || s.Cell.NoData {
			continue
		}
		// Tailwind for this bearing: projection of the cell's wind vector
		// onto the ride direction. WindBlowsTo is where wind pushes — if it
		// aligns with your bearing, it's a tailwind.
		diff := (s.Bearing - s.Cell.WindBlowsTo) * math.Pi / 180
		tail := s.Cell.WindSpeed * math.Cos(diff)
		rideable = append(rideable, scored{sector: s, DryHours: s.Cell.DryHours, Tailwind: tail})
	}

	if len(rideable) == 0 {
		fmt.Println(termplt.ColorRed + "No rideable direction found — everything around you is water or missing data." + termplt.ColorReset)
		return
	}

	// Best: most dry hours, tie-break on tailwind (higher = more favourable).
	bestIdx, worstIdx := 0, 0
	for i := 1; i < len(rideable); i++ {
		if rideable[i].DryHours > rideable[bestIdx].DryHours ||
			(rideable[i].DryHours == rideable[bestIdx].DryHours && rideable[i].Tailwind > rideable[bestIdx].Tailwind) {
			bestIdx = i
		}
		if rideable[i].DryHours < rideable[worstIdx].DryHours {
			worstIdx = i
		}
	}

	best := rideable[bestIdx]
	worst := rideable[worstIdx]

	b := termplt.ColorBold
	rst := termplt.ColorReset
	fmt.Printf("%sBest:%s head %s — %s, %s.\n",
		b, rst, best.Name, describeDry(best.DryHours, r.WindowHours), describeWind(best.Tailwind, best.Cell.WindSpeed))
	if worst.Name != best.Name && worst.DryHours < r.WindowHours {
		fmt.Printf("%sAvoid:%s %s — %s.\n",
			b, rst, worst.Name, describeDry(worst.DryHours, r.WindowHours))
	}
}

func describeDry(dryHours, windowHours int) string {
	switch {
	case dryHours >= windowHours:
		return fmt.Sprintf("dry the full %dh", windowHours)
	case dryHours == 0:
		return "raining now or within the hour"
	case dryHours == 1:
		return "dry for ~1h then rain"
	default:
		return fmt.Sprintf("dry for ~%dh then rain", dryHours)
	}
}

func describeWind(tailwind, windSpeed float64) string {
	if windSpeed <= windCalmKmh {
		return "calm"
	}
	abs := math.Abs(tailwind)
	var phrase string
	switch {
	case tailwind > tailHeadSwitchKmh:
		phrase = fmt.Sprintf("tailwind ~%.0f km/h", abs)
	case tailwind < -tailHeadSwitchKmh:
		phrase = fmt.Sprintf("headwind ~%.0f km/h", abs)
	default:
		phrase = "crosswind"
	}
	return phrase
}
