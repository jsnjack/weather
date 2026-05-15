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
	Width       int    // viewBox width
	Height      int    // viewBox height
	YUnit       string // e.g. "mm" or "°C"
	XTimeFormat string // e.g. "15:04"
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

	const padL, padR, padT, padB = 48, 12, 12, 28
	plotW := opts.Width - padL - padR
	plotH := opts.Height - padT - padB

	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" preserveAspectRatio="xMidYMid meet" role="img" aria-label="forecast chart" style="width:100%%;height:auto;font:12px system-ui,sans-serif">`,
		opts.Width, opts.Height)

	// Combined extents across non-empty series.
	var (
		minT, maxT       time.Time
		minV, maxV       float64
		any              bool
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

	// Y ticks: 5 evenly spaced.
	const yTicks = 5
	for i := 0; i <= yTicks; i++ {
		v := yLo + (yHi-yLo)*float64(i)/float64(yTicks)
		y := yPx(v)
		fmt.Fprintf(&b,
			`<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="currentColor" stroke-opacity="0.1"/>`,
			padL, y, padL+plotW, y)
		label := fmt.Sprintf("%.1f", v)
		if opts.YUnit != "" {
			label += opts.YUnit
		}
		fmt.Fprintf(&b,
			`<text x="%d" y="%.1f" text-anchor="end" dominant-baseline="middle" fill="currentColor" opacity="0.7">%s</text>`,
			padL-6, y, template.HTMLEscapeString(label))
	}

	// X ticks: snap to whole hours that fall inside [minT, maxT].
	startHour := minT.Truncate(time.Hour)
	if startHour.Before(minT) {
		startHour = startHour.Add(time.Hour)
	}
	for t := startHour; !t.After(maxT); t = t.Add(time.Hour) {
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
