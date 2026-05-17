package cmd

import (
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"
)

// SVGSeries is a single named, colored line for RenderLineChartSVG.
type SVGSeries struct {
	Name  string
	Color string
	Data  []ForecastDataPoint
}

// SVGOpts controls the SVG chart geometry and labels.
type SVGOpts struct {
	Width       int     // viewBox width
	Height      int     // viewBox height
	YUnit       string  // e.g. "mm/h" or "°C" — printed once above the axis
	XTimeFormat string  // e.g. "15:04"
	MinYHi      float64 // if non-zero, force the top of the y-axis to be at least this value
}

// RenderLineChartSVG returns an inline SVG <svg> element containing a
// time-series line chart for one or more series. The result is marked as safe
// HTML so it can be embedded directly in a template.
//
// Axis/text colors use `currentColor`, so the surrounding CSS controls them
// and the chart respects light/dark theme.
func RenderLineChartSVG(series []SVGSeries, opts SVGOpts) template.HTML {
	if opts.Width == 0 {
		opts.Width = 640
	}
	if opts.Height == 0 {
		opts.Height = 260
	}
	if opts.XTimeFormat == "" {
		opts.XTimeFormat = "15:04"
	}

	// padL must fit the widest y-axis label. With "nice" tick values we top
	// out at strings like "100" or "12.5" — ~5 chars × ~7 px + 6 px gap.
	const padL, padR, padT, padB = 44, 12, 22, 28
	plotW := opts.Width - padL - padR
	plotH := opts.Height - padT - padB

	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" preserveAspectRatio="xMidYMid meet" role="img" aria-label="forecast chart" style="width:100%%;height:auto;font:12px system-ui,sans-serif">`,
		opts.Width, opts.Height)

	// Combined extents across non-empty series.
	var (
		minT, maxT time.Time
		minV, maxV float64
		any        bool
	)
	for _, s := range series {
		for _, p := range s.Data {
			if !any {
				minT, maxT, minV, maxV, any = p.Time, p.Time, p.Value, p.Value, true
				continue
			}
			if p.Time.Before(minT) {
				minT = p.Time
			}
			if p.Time.After(maxT) {
				maxT = p.Time
			}
			if p.Value < minV {
				minV = p.Value
			}
			if p.Value > maxV {
				maxV = p.Value
			}
		}
	}

	if !any {
		fmt.Fprintf(&b,
			`<text x="%d" y="%d" text-anchor="middle" fill="currentColor" opacity="0.6">no data</text></svg>`,
			opts.Width/2, opts.Height/2)
		return template.HTML(b.String())
	}

	// Pad Y range so lines don't sit on the axes; ensure non-zero span.
	span := maxV - minV
	if span == 0 {
		span = math.Max(1, math.Abs(maxV))
	}
	yLo := minV - span*0.1
	yHi := maxV + span*0.1
	if minV >= 0 && yLo < 0 {
		yLo = 0 // anchor non-negative series (precipitation, probability) at zero
	}
	// Floor the top of the axis at MinYHi so a flat "no data / no rain" series
	// still produces a sensible-looking chart instead of collapsing the range
	// to ±0.1.
	if opts.MinYHi > 0 && yHi < opts.MinYHi {
		yHi = opts.MinYHi
	}
	// Snap yLo/yHi to "nice" round values (1/2/5 × 10^k) so tick labels read
	// like 0, 0.5, 1.0 rather than 0.02, 0.04, …
	tickStep := niceStep(yHi-yLo, 5)
	yLo = math.Floor(yLo/tickStep) * tickStep
	yHi = math.Ceil(yHi/tickStep) * tickStep
	if yHi == yLo {
		yHi = yLo + tickStep
	}
	// Decimal places appropriate for the tick step.
	tickDecimals := 0
	if tickStep < 1 {
		tickDecimals = int(math.Ceil(-math.Log10(tickStep)))
	}
	tickFmt := fmt.Sprintf("%%.%df", tickDecimals)

	xPx := func(t time.Time) float64 {
		span := float64(maxT.Sub(minT))
		if span == 0 {
			return float64(padL)
		}
		return float64(padL) + float64(t.Sub(minT))/span*float64(plotW)
	}
	yPx := func(v float64) float64 {
		return float64(padT) + float64(plotH) - (v-yLo)/(yHi-yLo)*float64(plotH)
	}

	// Axes.
	fmt.Fprintf(&b,
		`<g stroke="currentColor" stroke-opacity="0.3" fill="none"><line x1="%d" y1="%d" x2="%d" y2="%d"/><line x1="%d" y1="%d" x2="%d" y2="%d"/></g>`,
		padL, padT, padL, padT+plotH,
		padL, padT+plotH, padL+plotW, padT+plotH)

	// Unit caption above the y-axis (printed once instead of on every tick).
	if opts.YUnit != "" {
		fmt.Fprintf(&b,
			`<text x="%d" y="%d" text-anchor="end" fill="currentColor" opacity="0.6">%s</text>`,
			padL-6, padT-6, template.HTMLEscapeString(strings.TrimSpace(opts.YUnit)))
	}

	// Y ticks: walk yLo → yHi in tickStep increments.
	for v := yLo; v <= yHi+tickStep/2; v += tickStep {
		y := yPx(v)
		fmt.Fprintf(&b,
			`<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="currentColor" stroke-opacity="0.1"/>`,
			padL, y, padL+plotW, y)
		fmt.Fprintf(&b,
			`<text x="%d" y="%.1f" text-anchor="end" dominant-baseline="middle" fill="currentColor" opacity="0.7">%s</text>`,
			padL-6, y, template.HTMLEscapeString(fmt.Sprintf(tickFmt, v)))
	}

	// X ticks: pick a step that gives ~4-7 labels across the visible span.
	// A 2-hour chart on hour-only ticks ends up with two labels (e.g. 12:00,
	// 13:00); 30-minute ticks here keep the chart readable.
	step := pickTimeTickStep(maxT.Sub(minT))
	startTick := minT.Truncate(step)
	if startTick.Before(minT) {
		startTick = startTick.Add(step)
	}
	for t := startTick; !t.After(maxT); t = t.Add(step) {
		x := xPx(t)
		fmt.Fprintf(&b,
			`<line x1="%.1f" y1="%d" x2="%.1f" y2="%d" stroke="currentColor" stroke-opacity="0.15"/>`,
			x, padT, x, padT+plotH)
		fmt.Fprintf(&b,
			`<text x="%.1f" y="%d" text-anchor="middle" fill="currentColor" opacity="0.7">%s</text>`,
			x, padT+plotH+16, template.HTMLEscapeString(t.Format(opts.XTimeFormat)))
	}

	// Series lines.
	for _, s := range series {
		if len(s.Data) == 0 {
			continue
		}
		var pts strings.Builder
		for i, p := range s.Data {
			if i > 0 {
				pts.WriteByte(' ')
			}
			fmt.Fprintf(&pts, "%.1f,%.1f", xPx(p.Time), yPx(p.Value))
		}
		fmt.Fprintf(&b,
			`<polyline fill="none" stroke="%s" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" points="%s"/>`,
			template.HTMLEscapeString(s.Color), pts.String())
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// niceStep picks a "nice" tick step (1/2/5 × 10^k) that splits span into
// roughly targetTicks intervals. Produces tick values humans naturally
// read: 0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 50, …
func niceStep(span float64, targetTicks int) float64 {
	if span <= 0 || targetTicks <= 0 {
		return 1
	}
	raw := span / float64(targetTicks)
	pow := math.Pow(10, math.Floor(math.Log10(raw)))
	n := raw / pow
	switch {
	case n <= 1:
		return 1 * pow
	case n <= 2:
		return 2 * pow
	case n <= 5:
		return 5 * pow
	default:
		return 10 * pow
	}
}

// pickTimeTickStep returns a "nice" time-axis interval for a given span,
// chosen so the chart shows roughly 4-7 labels at a wall-clock-friendly
// cadence (15m, 30m, hourly, ...).
func pickTimeTickStep(span time.Duration) time.Duration {
	switch {
	case span <= time.Hour:
		return 15 * time.Minute
	case span <= 3*time.Hour:
		return 30 * time.Minute
	case span <= 6*time.Hour:
		return time.Hour
	case span <= 12*time.Hour:
		return 2 * time.Hour
	case span <= 24*time.Hour:
		return 3 * time.Hour
	default:
		return 6 * time.Hour
	}
}

// GridCell is one cell in a heat grid. Color is the background CSS color;
// Symbol is an optional center glyph (already a complete string like "↗" or
// "✗"); SymbolColor overrides the default text color. Border is drawn when
// non-empty, used for the start cell.
type GridCell struct {
	Color       string
	Symbol      string
	SymbolColor string
	Border      string
}

// GridOpts controls heat-grid layout.
type GridOpts struct {
	CellSize int     // px per cell in the viewBox; default 22
	StepKm   float64 // optional — when >0, axis labels show ±km on edges
	Title    string  // optional caption shown above the grid
}

// RenderHeatGridSVG draws cells[row][col] as a square grid. Row 0 is at the
// top of the SVG (matching the CLI heatmap where row 0 = north). The grid
// shows km-distance axis labels along the top and left edges when StepKm > 0.
func RenderHeatGridSVG(cells [][]GridCell, opts GridOpts) template.HTML {
	if len(cells) == 0 || len(cells[0]) == 0 {
		return template.HTML(`<svg viewBox="0 0 1 1"></svg>`)
	}
	if opts.CellSize == 0 {
		opts.CellSize = 22
	}
	rows := len(cells)
	cols := len(cells[0])
	mid := rows / 2

	padL := 36
	padT := 16
	padR, padB := 16, 16
	if opts.Title != "" {
		padT += 18
	}
	w := padL + cols*opts.CellSize + padR
	h := padT + rows*opts.CellSize + padB

	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" preserveAspectRatio="xMidYMid meet" role="img" style="width:100%%;height:auto;max-width:520px;font:11px system-ui,sans-serif">`,
		w, h)

	if opts.Title != "" {
		fmt.Fprintf(&b,
			`<text x="%d" y="14" font-weight="600" fill="currentColor">%s</text>`,
			padL, template.HTMLEscapeString(opts.Title))
	}

	// Axis: N/S km labels along left edge (one per row, only at edges and center to reduce clutter).
	if opts.StepKm > 0 {
		// Top row (north edge) and bottom row (south edge), plus middle = start.
		labelRows := map[int]string{
			0:        fmt.Sprintf("+%d km", int(float64(mid)*opts.StepKm)),
			mid:      "start",
			rows - 1: fmt.Sprintf("-%d km", int(float64(mid)*opts.StepKm)),
		}
		for r, txt := range labelRows {
			y := padT + r*opts.CellSize + opts.CellSize/2 + 4
			fmt.Fprintf(&b,
				`<text x="%d" y="%d" text-anchor="end" fill="currentColor" opacity="0.7">%s</text>`,
				padL-4, y, template.HTMLEscapeString(txt))
		}
		// W / E labels under the bottom-left and bottom-right.
		yLab := padT + rows*opts.CellSize + 12
		fmt.Fprintf(&b,
			`<text x="%d" y="%d" fill="currentColor" opacity="0.7">-%d W</text>`,
			padL, yLab, int(float64(mid)*opts.StepKm))
		fmt.Fprintf(&b,
			`<text x="%d" y="%d" text-anchor="end" fill="currentColor" opacity="0.7">+%d E</text>`,
			padL+cols*opts.CellSize, yLab, int(float64(mid)*opts.StepKm))
	}

	// Cells.
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			cell := cells[r][c]
			x := padL + c*opts.CellSize
			y := padT + r*opts.CellSize
			fmt.Fprintf(&b,
				`<rect x="%d" y="%d" width="%d" height="%d" fill="%s"`,
				x, y, opts.CellSize, opts.CellSize, template.HTMLEscapeString(cell.Color))
			if cell.Border != "" {
				fmt.Fprintf(&b, ` stroke="%s" stroke-width="2"`, template.HTMLEscapeString(cell.Border))
			}
			b.WriteString(`/>`)
			if cell.Symbol != "" {
				sc := cell.SymbolColor
				if sc == "" {
					sc = "#111"
				}
				fmt.Fprintf(&b,
					`<text x="%d" y="%d" text-anchor="middle" dominant-baseline="central" fill="%s" font-size="%d">%s</text>`,
					x+opts.CellSize/2, y+opts.CellSize/2+1,
					template.HTMLEscapeString(sc), opts.CellSize-6,
					template.HTMLEscapeString(cell.Symbol))
			}
		}
	}

	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}
