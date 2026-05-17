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
	DryHours    int     `json:"dryHours"`    // consecutive dry hours from ride start (0..windowHours)
	WindBlowsTo float64 `json:"windBlowsTo"` // degrees, 0=N — where wind is pushing you (midpoint hour)
	WindSpeed   float64 `json:"windSpeed"`   // km/h, midpoint hour sustained wind
	Sea         bool    `json:"sea"`
	NoData      bool    `json:"noData"`
}

// hourlyWind holds a single hour's wind observation for the evolution strip.
type hourlyWind struct {
	BlowsTo float64 `json:"blowsTo"` // degrees, 0 = N — where wind is pushing
	Speed   float64 `json:"speed"`   // km/h, sustained
}

// sectorEvolution is the per-hour wind sequence for one compass sector,
// sampled at half-radius from the start.
type sectorEvolution struct {
	Name      string       `json:"name"`
	Wind      []hourlyWind `json:"wind"` // one entry per hour of the ride window
	OverWater bool         `json:"overWater"`
	NoData    bool         `json:"noData"`
}

type todayResult struct {
	Cells       [][]todayCell     `json:"cells"` // [row][col]; row 0 = north
	StepKm      float64           `json:"stepKm"`
	RadiusKm    float64           `json:"radiusKm"`
	Grid        int               `json:"grid"`
	StartLat    float64           `json:"startLat"`
	StartLon    float64           `json:"startLon"`
	StartTime   time.Time         `json:"startTime"`
	WindowHours int               `json:"windowHours"`
	Sectors     []sectorEvolution `json:"sectors"` // 8 entries in compass order: N, NE, E, SE, S, SW, W, NW
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

	prog := NewCLIProgress("forecast cells")
	result := runTodayGrid(loc.Latitude, loc.Longitude, startTime, FlagTodayHours, todayGridSize(), FlagTodayRadius, prog)
	prog.Finish()
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
func runTodayGrid(startLat, startLon float64, startTime time.Time, windowHours, gridSize int, radiusKm float64, prog Progress) todayResult {
	if gridSize < 5 {
		gridSize = 5
	}
	if gridSize%2 == 0 {
		gridSize++
	}
	stepKm := radiusKm / float64(gridSize/2)

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
	prog.AddTotal(len(cells))
	for i, c := range cells {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c cellCoord) {
			defer wg.Done()
			defer func() { <-sem }()
			defer prog.Inc(1)
			data, err := GetOpenMeteoRange(c.Lat, c.Lon, startDate, endDate)
			results[i] = cellData{Row: c.Row, Col: c.Col, Data: data, Err: err}
		}(i, c)
	}
	wg.Wait()

	out := todayResult{
		StepKm:      stepKm,
		RadiusKm:    radiusKm,
		Grid:        gridSize,
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
	gridSize := r.Grid
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
	// Use the same sea-cyan tint as the rest of the heatmap when the start
	// itself sits on water (rare, but possible if the user drops a lat/lon
	// on the IJsselmeer or an IP geocode puts them in the North Sea).
	if isStart {
		bg := todayBg(rainTimingBand(c.DryHours, windowHours))
		if c.NoData {
			bg = termplt.ColorBackgroundBrightBlack
		}
		fg := termplt.ColorBold + termplt.ColorWhite
		if c.Sea {
			fg = termplt.ColorBold + termplt.ColorCyan
		}
		return bg + fg + " ●" + rst
	}
	if c.NoData {
		return termplt.ColorBackgroundBrightBlack + "  " + rst
	}

	band := rainTimingBand(c.DryHours, windowHours)
	bg := todayBg(band)

	if band == 3 {
		// Raining now / within 1h. Tint the ✗ cyan if this cell is over water,
		// so the "you can't ride here anyway" info isn't lost behind the
		// rain colour.
		fg := termplt.ColorBold + termplt.ColorWhite
		if c.Sea {
			fg = termplt.ColorBold + termplt.ColorCyan
		}
		return bg + fg + " ✗" + rst
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
	gridSize := r.Grid
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
		// Sector name is cyan when over water so the row reads as "water"
		// from the very first column, matching how individual cells are tinted.
		if sec.OverWater {
			fmt.Printf("  %s%-4s%s  ", termplt.ColorCyan, sec.Name, rst)
		} else {
			fmt.Printf("  %-4s  ", sec.Name)
		}
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
	fmt.Printf("    %s    over water, dry (weather still shown; you can't ride there)\n",
		termplt.ColorBackgroundBrightGreen+termplt.ColorCyan+"→·"+rst)
	fmt.Printf("    %s    over water, raining (cyan ✗ on rain background)\n",
		termplt.ColorBackgroundRed+b+termplt.ColorCyan+" ✗"+rst)
	fmt.Printf("    %s    your starting point\n",
		termplt.ColorBackgroundBrightGreen+b+termplt.ColorWhite+" ●"+rst)
	fmt.Printf("    %s    no data from the forecast provider (try refreshing)\n",
		termplt.ColorBackgroundBrightBlack+"  "+rst)
}

// TodaySectorScore is one compass direction's rideability summary at
// half-radius from the start. Used for the recommendation.
type TodaySectorScore struct {
	Name     string    `json:"name"`
	Bearing  float64   `json:"bearing"`
	DryHours int       `json:"dryHours"`
	Tailwind float64   `json:"tailwind"` // km/h along bearing (positive = tailwind)
	Cell     todayCell `json:"cell"`
}

// TodayRecommendation is the pure (no-stdout) result of evaluating the 8
// compass sectors. Best/Worst are zero-valued when Rideable is empty.
type TodayRecommendation struct {
	Rideable []TodaySectorScore `json:"rideable"`
	Best     TodaySectorScore   `json:"best"`
	Worst    TodaySectorScore   `json:"worst"`
}

// RecommendToday samples 8 compass sectors at half-radius and ranks them.
// Pure function: no rendering. The CLI renderer and the HTTP handler both
// call this and present the result in their own way.
func RecommendToday(r todayResult) TodayRecommendation {
	mid := r.Grid / 2
	half := mid / 2
	if half < 1 {
		half = 1
	}
	probes := []TodaySectorScore{
		{Name: "N", Bearing: 0, Cell: r.Cells[mid-half][mid]},
		{Name: "NE", Bearing: 45, Cell: r.Cells[mid-half][mid+half]},
		{Name: "E", Bearing: 90, Cell: r.Cells[mid][mid+half]},
		{Name: "SE", Bearing: 135, Cell: r.Cells[mid+half][mid+half]},
		{Name: "S", Bearing: 180, Cell: r.Cells[mid+half][mid]},
		{Name: "SW", Bearing: 225, Cell: r.Cells[mid+half][mid-half]},
		{Name: "W", Bearing: 270, Cell: r.Cells[mid][mid-half]},
		{Name: "NW", Bearing: 315, Cell: r.Cells[mid-half][mid-half]},
	}

	out := TodayRecommendation{}
	for _, s := range probes {
		if s.Cell.Sea || s.Cell.NoData {
			continue
		}
		// Tailwind: projection of cell wind onto bearing; positive = push.
		diff := (s.Bearing - s.Cell.WindBlowsTo) * math.Pi / 180
		s.Tailwind = s.Cell.WindSpeed * math.Cos(diff)
		s.DryHours = s.Cell.DryHours
		out.Rideable = append(out.Rideable, s)
	}
	if len(out.Rideable) == 0 {
		return out
	}

	bestIdx, worstIdx := 0, 0
	for i := 1; i < len(out.Rideable); i++ {
		if out.Rideable[i].DryHours > out.Rideable[bestIdx].DryHours ||
			(out.Rideable[i].DryHours == out.Rideable[bestIdx].DryHours && out.Rideable[i].Tailwind > out.Rideable[bestIdx].Tailwind) {
			bestIdx = i
		}
		if out.Rideable[i].DryHours < out.Rideable[worstIdx].DryHours {
			worstIdx = i
		}
	}
	out.Best = out.Rideable[bestIdx]
	out.Worst = out.Rideable[worstIdx]
	return out
}

func printTodayRecommendation(r todayResult) {
	rec := RecommendToday(r)
	b := termplt.ColorBold
	rst := termplt.ColorReset
	if len(rec.Rideable) == 0 {
		fmt.Println(termplt.ColorRed + "No rideable direction found — everything around you is water or missing data." + termplt.ColorReset)
		return
	}
	fmt.Printf("%sBest:%s head %s — %s, %s.\n",
		b, rst, rec.Best.Name, describeDry(rec.Best.DryHours, r.WindowHours), describeWind(rec.Best.Tailwind, rec.Best.Cell.WindSpeed))
	if rec.Worst.Name != rec.Best.Name && rec.Worst.DryHours < r.WindowHours {
		fmt.Printf("%sAvoid:%s %s — %s.\n",
			b, rst, rec.Worst.Name, describeDry(rec.Worst.DryHours, r.WindowHours))
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
