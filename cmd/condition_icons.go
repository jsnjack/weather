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
// widget share one line-icon set. TestConditionIconsMatchWidget keeps the two
// copies identical; change the XML and regenerate this table together.
var conditionIconPaths = map[string][]conditionIconPath{
	"clear": {
		{D: "M12,6.5A5.5,5.5 0 1 0 12,17.5A5.5,5.5 0 0 0 12,6.5ZM12,16A4,4 0 1 1 12,8A4,4 0 0 1 12,16Z", Fill: true, Stroke: 0},
		{D: "M11.25,2L11.25,4L12.75,4L12.75,2ZM11.25,20L11.25,22L12.75,22L12.75,20ZM2,11.25L2,12.75L4,12.75L4,11.25ZM20,11.25L20,12.75L22,12.75L22,11.25Z", Fill: true, Stroke: 0},
		{D: "M4.93,4.22L6.34,5.64L7.05,4.93L5.64,3.52ZM16.95,16.95L18.36,18.36L19.07,17.66L17.66,16.24ZM16.95,7.05L18.36,5.64L17.66,4.93L16.24,6.34ZM4.93,19.78L6.34,18.36L5.64,17.66L4.22,19.07Z", Fill: true, Stroke: 0},
	},
	"partly_cloudy": {
		{D: "M9,3.5L9,5L10,5L10,3.5ZM5.2,5.2L4.5,5.9L5.6,7L6.3,6.3ZM2.5,8.5L2.5,9.5L4,9.5L4,8.5ZM13.8,5.9L13.1,5.2L12,6.3L12.7,7Z", Fill: true, Stroke: 0},
		{D: "M9,6.5A3.5,3.5 0 0 0 5.51,9.8 6.5,6.5 0 0 1 8,9.5 6.5,6.5 0 0 1 12.25,11A3.5,3.5 0 0 0 9,6.5Z", Fill: true, Stroke: 0},
		{D: "M16.5,13.5C16.5,11.84 15.16,10.5 13.5,10.5 13.18,10.5 12.87,10.55 12.58,10.65 11.94,9.65 10.81,9 9.5,9 7.91,9 6.59,9.96 6.13,11.32 4.95,11.42 4,12.4 4,13.62 4,14.92 5.08,16 6.38,16L13.62,16C15.07,16 16.25,14.95 16.48,13.6 16.49,13.57 16.5,13.53 16.5,13.5ZM13.62,14.5L6.38,14.5C5.9,14.5 5.5,14.1 5.5,13.62 5.5,13.21 5.81,12.85 6.22,12.78L7.21,12.62 7.55,11.68C7.84,10.86 8.62,10.5 9.5,10.5 10.31,10.5 11.04,10.92 11.46,11.59L12.07,12.55 13.13,12.12C13.25,12.07 13.37,12.04 13.5,12 14.33,12 15,12.67 15,13.5 15,13.61 14.99,13.71 14.97,13.81L14.92,14.09 14.78,14.32C14.55,14.43 14.32,14.5 13.62,14.5Z", Fill: true, Stroke: 0},
	},
	"overcast": {
		{D: "M19.36,10.04C18.67,6.59 15.64,4 12,4 9.11,4 6.6,5.64 5.35,8.04 2.34,8.36 0,10.91 0,14a6,6 0 0 0 6,6h13a5,5 0 0 0 0.36,-9.96zM19,18H6c-2.21,0 -4,-1.79 -4,-4 0,-2.05 1.53,-3.76 3.56,-3.97l1.07,-0.11 0.5,-0.95C8.08,7.14 9.94,6 12,6c2.62,0 4.88,1.86 5.39,4.43l0.3,1.5 1.53,0.11A2.98,2.98 0 0 1 22,15c0,1.65 -1.35,3 -3,3z", Fill: true, Stroke: 0},
	},
	"fog": {
		{D: "M3,15h13a1,1 0 0 0 0,-2H3a1,1 0 0 0 0,2zM3,19h18a1,1 0 0 0 0,-2H3a1,1 0 0 0 0,2zM3,11h18a1,1 0 0 0 0,-2H3a1,1 0 0 0 0,2zM3,7h13a1,1 0 0 0 0,-2H3a1,1 0 0 0 0,2z", Fill: true, Stroke: 0},
	},
	"drizzle": {
		{D: "M19.36,7.04C18.67,3.59 15.64,1 12,1 9.11,1 6.6,2.64 5.35,5.04 2.34,5.36 0,7.91 0,11a5,5 0 0 0 5,5h14a5,5 0 0 0 0.36,-8.96zM19,14H5c-1.65,0 -3,-1.35 -3,-3 0,-2.05 1.53,-3.76 3.56,-3.97L6.63,6.92 7.13,5.97C8.08,4.14 9.94,3 12,3c2.62,0 4.88,1.86 5.39,4.43l0.3,1.5 1.53,0.11A2.98,2.98 0 0 1 22,11c0,1.65 -1.35,3 -3,3z", Fill: true, Stroke: 0},
		{D: "M7,18m-1,0a1,1 0 1,1 2,0a1,1 0 1,1 -2,0M12,19m-1,0a1,1 0 1,1 2,0a1,1 0 1,1 -2,0M17,18m-1,0a1,1 0 1,1 2,0a1,1 0 1,1 -2,0M9.5,21m-1,0a1,1 0 1,1 2,0a1,1 0 1,1 -2,0M14.5,21m-1,0a1,1 0 1,1 2,0a1,1 0 1,1 -2,0", Fill: true, Stroke: 0},
	},
	"rain": {
		{D: "M19.36,7.04C18.67,3.59 15.64,1 12,1 9.11,1 6.6,2.64 5.35,5.04 2.34,5.36 0,7.91 0,11a5,5 0 0 0 5,5h14a5,5 0 0 0 0.36,-8.96zM19,14H5c-1.65,0 -3,-1.35 -3,-3 0,-2.05 1.53,-3.76 3.56,-3.97L6.63,6.92 7.13,5.97C8.08,4.14 9.94,3 12,3c2.62,0 4.88,1.86 5.39,4.43l0.3,1.5 1.53,0.11A2.98,2.98 0 0 1 22,11c0,1.65 -1.35,3 -3,3z", Fill: true, Stroke: 0},
		{D: "M6.5,17.5L5.5,21.5L7,21.5L8,17.5ZM11.5,17.5L10.5,21.5L12,21.5L13,17.5ZM16.5,17.5L15.5,21.5L17,21.5L18,17.5Z", Fill: true, Stroke: 0},
	},
	"snow": {
		{D: "M19.36,7.04C18.67,3.59 15.64,1 12,1 9.11,1 6.6,2.64 5.35,5.04 2.34,5.36 0,7.91 0,11a5,5 0 0 0 5,5h14a5,5 0 0 0 0.36,-8.96zM19,14H5c-1.65,0 -3,-1.35 -3,-3 0,-2.05 1.53,-3.76 3.56,-3.97L6.63,6.92 7.13,5.97C8.08,4.14 9.94,3 12,3c2.62,0 4.88,1.86 5.39,4.43l0.3,1.5 1.53,0.11A2.98,2.98 0 0 1 22,11c0,1.65 -1.35,3 -3,3z", Fill: true, Stroke: 0},
		{D: "M7,17.5L7,21.5M5,18.5L9,20.5M9,18.5L5,20.5", Fill: true, Stroke: 0},
		{D: "M7,17.5L7,21.5M5,18.5L9,20.5M9,18.5L5,20.5M12,17.5L12,21.5M10,18.5L14,20.5M14,18.5L10,20.5M17,17.5L17,21.5M15,18.5L19,20.5M19,18.5L15,20.5", Fill: true, Stroke: 0.9},
	},
	"thunderstorm": {
		{D: "M19.36,7.04C18.67,3.59 15.64,1 12,1 9.11,1 6.6,2.64 5.35,5.04 2.34,5.36 0,7.91 0,11a5,5 0 0 0 5,5h14a5,5 0 0 0 0.36,-8.96zM19,14H5c-1.65,0 -3,-1.35 -3,-3 0,-2.05 1.53,-3.76 3.56,-3.97L6.63,6.92 7.13,5.97C8.08,4.14 9.94,3 12,3c2.62,0 4.88,1.86 5.39,4.43l0.3,1.5 1.53,0.11A2.98,2.98 0 0 1 22,11c0,1.65 -1.35,3 -3,3z", Fill: true, Stroke: 0},
		{D: "M12.5,17L8,21l3,-3.5L9,17.5l4.5,-4L11,17z", Fill: true, Stroke: 0},
	},
	"night": {
		{D: "M14.5,3.6A8.5,8.5 0 1 0 20.4,15.6 7,7 0 0 1 14.5,3.6Z", Fill: false, Stroke: 1.6},
	},
}

// conditionIconKey picks the icon for a wmoCondition token: at night, clear
// shows the moon and partly cloudy plain cloud, as on the widget.
func conditionIconKey(condition string, night bool) string {
	if night {
		switch condition {
		case "clear", "":
			return "night"
		case "partly_cloudy":
			return "overcast"
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
