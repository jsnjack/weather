package cmd

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestConditionIconsMatchWidget checks that the web's inline SVG icons are the
// Android widget's vector drawables, path for path.
func TestConditionIconsMatchWidget(t *testing.T) {
	type vectorPath struct {
		Data        string `xml:"http://schemas.android.com/apk/res/android pathData,attr"`
		FillColor   string `xml:"http://schemas.android.com/apk/res/android fillColor,attr"`
		StrokeWidth string `xml:"http://schemas.android.com/apk/res/android strokeWidth,attr"`
	}
	type vector struct {
		Paths []vectorPath `xml:"path"`
	}
	for name, want := range conditionIconPaths {
		body, err := os.ReadFile(filepath.Join("..", "android", "app", "src", "main", "res", "drawable", "ic_cond_"+name+".xml"))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var v vector
		if err := xml.Unmarshal(body, &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(v.Paths) != len(want) {
			t.Fatalf("%s: widget has %d paths, web has %d", name, len(v.Paths), len(want))
		}
		for i, p := range v.Paths {
			stroke := 0.0
			if p.StrokeWidth != "" {
				if stroke, err = strconv.ParseFloat(p.StrokeWidth, 64); err != nil {
					t.Fatalf("%s path %d stroke: %v", name, i, err)
				}
			}
			fill := p.FillColor != "#00000000"
			if p.Data != want[i].D || fill != want[i].Fill || stroke != want[i].Stroke {
				t.Errorf("%s path %d differs from the widget drawable", name, i)
			}
		}
	}
	for _, token := range []string{"clear", "partly_cloudy", "overcast", "fog", "drizzle", "rain", "snow", "thunderstorm"} {
		if _, ok := conditionIconPaths[token]; !ok {
			t.Errorf("no icon for wmoCondition token %q", token)
		}
	}
}

func TestConditionIconSVG(t *testing.T) {
	svg := string(conditionIcon("clear", true))
	if !strings.HasPrefix(svg, `<svg class="cond-ic"`) || !strings.Contains(svg, `aria-hidden="true"`) {
		t.Fatalf("icon markup = %s", svg)
	}
	if !strings.Contains(svg, `fill="none"`) || !strings.Contains(svg, `stroke="currentColor"`) {
		t.Fatalf("night icon should be the stroked moon: %s", svg)
	}
	if got := conditionIconKey("unknown", false); got != "clear" {
		t.Fatalf("unknown condition = %q, want clear", got)
	}
}
