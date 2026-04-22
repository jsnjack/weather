package cmd

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/jsnjack/termplt"
)

// heatmapGridSize returns the effective grid size: the flag value, forced odd
// so the start sits on a cell center, and clamped to a sensible minimum.
func heatmapGridSize() int {
	n := FlagScoutHeatmapGrid
	if n < 5 {
		n = 5
	}
	if n%2 == 0 {
		n++
	}
	return n
}

// cellStatus is what renderHeatmap shows for one (cell, day). Option A
// encoding: background colour from TempBand, symbol from WindBand. Rain,
// severe gust, sea, and missing-data are overrides that replace both.
type cellStatus struct {
	TempBand int // 0 = cold .. 3 = ideal (ignored when an override is set)
	WindBand int // 0 = calm .. 3 = strong
	Rain     bool
	Gust     bool
	Sea      bool
	NoData   bool
}

// heatmapResult is the full result: days × rows × cols, plus the axis step
// in km so renderHeatmap can print distance labels.
type heatmapResult struct {
	Days     []time.Time
	StepKm   float64 // km between adjacent cells
	Cells    [][][]cellStatus // [day][row][col]; row 0 = north, col 0 = west
	StartLat float64
	StartLon float64
}

// RunHeatmap builds a grid of sample points around (startLat, startLon),
// fetches a multi-day forecast for each, and scores each (cell, day) with
// ScoreDayOmni.
func RunHeatmap(startLat, startLon float64, startDate time.Time, days int, cfg beamConfig) heatmapResult {
	gridSize := heatmapGridSize()
	halfSpanKm := float64(days) * cfg.KmPerDay
	stepKm := halfSpanKm / float64(gridSize/2)

	type cellCoord struct {
		Lat, Lon float64
		Row, Col int
	}
	cells := make([]cellCoord, 0, gridSize*gridSize)
	latStep := stepKm / 111.0 // ~1° lat = 111 km
	lonFactor := 111.0 * math.Cos(startLat*math.Pi/180)
	if lonFactor < 1 {
		lonFactor = 1
	}
	lonStep := stepKm / lonFactor

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

	endDate := startDate.AddDate(0, 0, days-1)

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

	out := heatmapResult{
		StepKm:   stepKm,
		StartLat: startLat,
		StartLon: startLon,
		Days:     make([]time.Time, days),
		Cells:    make([][][]cellStatus, days),
	}
	for d := 0; d < days; d++ {
		out.Days[d] = startDate.AddDate(0, 0, d)
		out.Cells[d] = make([][]cellStatus, gridSize)
		for row := 0; row < gridSize; row++ {
			out.Cells[d][row] = make([]cellStatus, gridSize)
		}
	}

	for _, rd := range results {
		if rd.Err != nil || rd.Data == nil || len(rd.Data.Hourly) == 0 {
			for d := 0; d < days; d++ {
				out.Cells[d][rd.Row][rd.Col] = cellStatus{NoData: true}
			}
			continue
		}
		if rd.Data.IsSea() {
			for d := 0; d < days; d++ {
				out.Cells[d][rd.Row][rd.Col] = cellStatus{Sea: true}
			}
			continue
		}
		// Bucket hourlies by date (YYYY-MM-DD) so ScoreDayOmni sees one day at a time.
		byDate := map[string][]HourlyForecast{}
		for _, h := range rd.Data.Hourly {
			k := h.Time.Format("2006-01-02")
			byDate[k] = append(byDate[k], h)
		}
		for d := 0; d < days; d++ {
			date := startDate.AddDate(0, 0, d).Format("2006-01-02")
			ds := ScoreDayOmni(byDate[date], cfg.MinTemp)
			status := cellStatus{
				TempBand: TempBand(ds.MaxTemp, cfg.MinTemp),
				WindBand: WindBand(ds.MaxSustainedWind),
			}
			if ds.Disqualified {
				switch ds.Reason {
				case "RAIN":
					status.Rain = true
				case "GUST":
					status.Gust = true
				default:
					status.NoData = true
				}
			}
			out.Cells[d][rd.Row][rd.Col] = status
		}
	}
	return out
}

// renderHeatmap prints one small map per day, stacked vertically, with a
// legend at the bottom.
func renderHeatmap(h heatmapResult) {
	gridSize := heatmapGridSize()
	mid := gridSize / 2
	for d, day := range h.Days {
		fmt.Printf("%sDay %d  %s%s\n",
			termplt.ColorBold, d+1, day.Format("2006-01-02"), termplt.ColorReset,
		)
		for row := 0; row < gridSize; row++ {
			fmt.Print("  ")
			for col := 0; col < gridSize; col++ {
				cell := h.Cells[d][row][col]
				isStart := row == mid && col == mid
				fmt.Print(heatmapCellGlyph(cell, isStart))
			}
			northCells := mid - row
			fmt.Printf("  %+4d km\n", int(float64(northCells)*h.StepKm))
		}
		// Bottom axis label (E/W distances at the edges, "start" in the middle).
		fmt.Print("  ")
		eastEdge := int(float64(mid) * h.StepKm)
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
		fmt.Println()
	}
	renderHeatmapLegend()
}

// heatmapCellGlyph returns the 2-char rendered cell.
//   - Background = temperature band (or override colour for rain/gust/sea).
//   - 2nd char   = wind symbol (space / · / ~ / ≈).
//   - Start cell gets a ● overlay that replaces the wind symbol.
func heatmapCellGlyph(c cellStatus, isStart bool) string {
	bg := heatmapBg(c)
	symbol := " " + heatmapWindSymbol(c)
	fg := ""

	switch {
	case c.Sea:
		symbol = "~~"
		fg = termplt.ColorCyan
	case c.Rain:
		symbol = " ·"
		fg = termplt.ColorBlack
	case c.Gust:
		symbol = " ✗"
		fg = termplt.ColorBold + termplt.ColorWhite
	case c.NoData:
		symbol = "  "
	}
	if isStart {
		symbol = " ●"
		fg = termplt.ColorBold + termplt.ColorWhite
	}
	return bg + fg + symbol + termplt.ColorReset
}

// heatmapBg picks the 2-char background colour for a cell.
func heatmapBg(c cellStatus) string {
	switch {
	case c.NoData:
		return termplt.ColorBackgroundBrightBlack
	case c.Sea:
		return termplt.ColorBackgroundCyan
	case c.Rain:
		return termplt.ColorBackgroundBlue
	case c.Gust:
		return termplt.ColorBackgroundRed
	}
	switch c.TempBand {
	case 3:
		return termplt.ColorBackgroundBrightGreen
	case 2:
		return termplt.ColorBackgroundGreen
	case 1:
		return termplt.ColorBackgroundYellow
	default:
		return termplt.ColorBackgroundBrightBlack
	}
}

// heatmapWindSymbol picks the glyph that fills the 2nd char of a cell for
// dry cells. No glyph (space) = calm.
func heatmapWindSymbol(c cellStatus) string {
	switch c.WindBand {
	case 0:
		return " "
	case 1:
		return "·"
	case 2:
		return "~"
	default:
		return "≈"
	}
}

func renderHeatmapLegend() {
	rst := termplt.ColorReset
	sw := func(bg, body string) string { return bg + body + rst }
	min := FlagScoutMinTemp
	b := termplt.ColorBold
	fmt.Println(b + "Legend" + rst + " — background = temperature, symbol = wind:")
	fmt.Println()
	fmt.Println(b + "  Temperature (cell background)" + rst)
	fmt.Printf("    %s    ideal, ≥%.0f°C\n", sw(termplt.ColorBackgroundBrightGreen, "   "), min+5)
	fmt.Printf("    %s    warm, %.0f–%.0f°C\n", sw(termplt.ColorBackgroundGreen, "   "), min, min+5)
	fmt.Printf("    %s    cool, %.0f–%.0f°C\n", sw(termplt.ColorBackgroundYellow, "   "), min-5, min)
	fmt.Printf("    %s    cold, below %.0f°C\n", sw(termplt.ColorBackgroundBrightBlack, "   "), min-5)
	fmt.Println()
	fmt.Println(b + "  Wind (symbol in cell)" + rst)
	fmt.Printf("    %s    calm, ≤%.0f km/h\n", sw(termplt.ColorBackgroundGreen, "   "), windCalmKmh)
	fmt.Printf("    %s    breezy, %.0f–%.0f km/h\n", sw(termplt.ColorBackgroundGreen, " · "), windCalmKmh, windBreezyKmh)
	fmt.Printf("    %s    windy, %.0f–%.0f km/h\n", sw(termplt.ColorBackgroundGreen, " ~ "), windBreezyKmh, windWindyKmh)
	fmt.Printf("    %s    strong, %.0f–%.0f km/h\n", sw(termplt.ColorBackgroundGreen, " ≈ "), windWindyKmh, windStrongKmh)
	fmt.Println()
	fmt.Println(b + "  Overrides" + rst)
	fmt.Printf("    %s    any daytime rain — skip this day\n", sw(termplt.ColorBackgroundBlue, " · "))
	fmt.Printf("    %s    gust ≥%.0f km/h — skip this day\n", sw(termplt.ColorBackgroundRed, " ✗ "), gustDisqualify)
	fmt.Printf("    %s    over water — not rideable\n", sw(termplt.ColorBackgroundCyan, "~~ "))
	fmt.Printf("    %s    your starting point (overlaid on whichever colour that cell would be)\n",
		termplt.ColorBackgroundBrightGreen+b+termplt.ColorWhite+" ● "+rst)
}
