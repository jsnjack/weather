package cmd

import (
	"fmt"
	"html/template"
	"strings"
)

// conditionIconPath is one path of a 24×24 condition icon.
type conditionIconPath struct {
	D      string
	Fill   bool    // filled with the text colour
	Stroke float64 // stroke width in viewport units; 0 for none
}

// conditionIconPaths are the Android widget's condition icons
// (android/app/src/main/res/drawable/ic_cond_*.xml), so the web pages and the
// widget share one line-icon set: a 2-unit round stroke on a 24-unit grid and
// one silhouette per condition, legible at 16 px. TestConditionIconsMatchWidget
// keeps the two copies identical; change the XML and this table together.
var conditionIconPaths = map[string][]conditionIconPath{
	"clear": {
		{D: "M12 8A4 4 0 1 0 12 16A4 4 0 1 0 12 8Z", Fill: false, Stroke: 2},
		{D: "M12 2.5V5M18.72 5.28L16.95 7.05M21.5 12H19M18.72 18.72L16.95 16.95M12 21.5V19M5.28 18.72L7.05 16.95M2.5 12H5M5.28 5.28L7.05 7.05", Fill: false, Stroke: 2},
	},
	"night": {
		{D: "M19.5 14.5A8.2 8.2 0 1 1 9.5 4.5A7.2 7.2 0 0 0 19.5 14.5Z", Fill: false, Stroke: 2},
	},
	"partly_cloudy": {
		{D: "M9.5 20A3.5 3.5 0 0 1 9 13.04A4.8 4.8 0 0 1 17.9 11.9A4.1 4.1 0 0 1 17.6 20Z", Fill: false, Stroke: 2},
		{D: "M5.6 9.6A3 3 0 1 1 10.4 6.4", Fill: false, Stroke: 2},
		{D: "M8 1.8V3.5M3.62 3.62L4.82 4.82M1.8 8H3.5M12.38 3.62L11.18 4.82M3.62 12.38L4.82 11.18", Fill: false, Stroke: 2},
	},
	"partly_cloudy_night": {
		{D: "M9.5 20A3.5 3.5 0 0 1 9 13.04A4.8 4.8 0 0 1 17.9 11.9A4.1 4.1 0 0 1 17.6 20Z", Fill: false, Stroke: 2},
		{D: "M11.2 9.3A4.4 4.4 0 1 1 6.7 3.4A3.8 3.8 0 0 0 11.2 9.3Z", Fill: false, Stroke: 2},
	},
	"overcast": {
		{D: "M6.5 19A4.5 4.5 0 0 1 5.9 10.04A6.2 6.2 0 0 1 17.3 9.5A4.8 4.8 0 0 1 17.5 19Z", Fill: false, Stroke: 2},
	},
	"fog": {
		{D: "M7 15A4 4 0 0 1 6.5 7.03A5.5 5.5 0 0 1 16.7 6.6A4.25 4.25 0 0 1 17 15Z", Fill: false, Stroke: 2},
		{D: "M3.5 18.5H16.5M7.5 21.5H20.5", Fill: false, Stroke: 2},
	},
	"drizzle": {
		{D: "M7 15A4 4 0 0 1 6.5 7.03A5.5 5.5 0 0 1 16.7 6.6A4.25 4.25 0 0 1 17 15Z", Fill: false, Stroke: 2},
		{D: "M8 18.6V18.61M12 18.6V18.61M16 18.6V18.61M10 21.8V21.81M14 21.8V21.81", Fill: false, Stroke: 2.6},
	},
	"rain": {
		{D: "M7 15A4 4 0 0 1 6.5 7.03A5.5 5.5 0 0 1 16.7 6.6A4.25 4.25 0 0 1 17 15Z", Fill: false, Stroke: 2},
		{D: "M8.5 18L7 22.5M12.5 18L11 22.5M16.5 18L15 22.5", Fill: false, Stroke: 2},
	},
	"snow": {
		{D: "M7 15A4 4 0 0 1 6.5 7.03A5.5 5.5 0 0 1 16.7 6.6A4.25 4.25 0 0 1 17 15Z", Fill: false, Stroke: 2},
		{D: "M8 18.2V22.2M6.27 19.2L9.73 21.2M6.27 21.2L9.73 19.2M16 18.2V22.2M14.27 19.2L17.73 21.2M14.27 21.2L17.73 19.2", Fill: false, Stroke: 1.5},
	},
	"thunderstorm": {
		{D: "M7 15A4 4 0 0 1 6.5 7.03A5.5 5.5 0 0 1 16.7 6.6A4.25 4.25 0 0 1 17 15H14.6M10.2 15H7", Fill: false, Stroke: 2},
		{D: "M13.6 12.8L9.6 18.2H12.7L11.2 22.8L15.8 16.8H12.6L14.2 12.8Z", Fill: true, Stroke: 1.3},
	},
}

// conditionIconKey picks the icon for a wmoCondition token: at night, clear
// shows the moon and partly cloudy the moon behind a cloud, as on the widget.
func conditionIconKey(condition string, night bool) string {
	if night {
		switch condition {
		case "clear", "":
			return "night"
		case "partly_cloudy":
			return "partly_cloudy_night"
		}
	}
	if _, ok := conditionIconPaths[condition]; ok {
		return condition
	}
	return "clear"
}

// conditionIcon renders a condition as an inline SVG line icon in the
// current text colour. It is decorative: callers give the accessible name.
func conditionIcon(condition string, night bool) template.HTML {
	var b strings.Builder
	b.WriteString(`<svg class="cond-ic" viewBox="0 0 24 24" aria-hidden="true" focusable="false">`)
	for _, p := range conditionIconPaths[conditionIconKey(condition, night)] {
		fill := "currentColor"
		if !p.Fill {
			fill = "none"
		}
		fmt.Fprintf(&b, `<path d="%s" fill="%s"`, p.D, fill)
		if p.Stroke > 0 {
			fmt.Fprintf(&b, ` stroke="currentColor" stroke-width="%g" stroke-linecap="round" stroke-linejoin="round"`, p.Stroke)
		}
		b.WriteString(`/>`)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String()) //nolint:gosec // fixed path data from this file, no user input
}
