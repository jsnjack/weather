package cmd

import "testing"

func TestVisibleWidth(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"plain ascii", "hello", 5},
		{"empty", "", 0},
		{"ansi color wrapper", "\x1b[31mred\x1b[0m", 3},
		{"ansi multiple wrappers", "\x1b[1m\x1b[31mhi\x1b[0m", 2},
		{"unicode rune", "é", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := visibleWidth(tc.in)
			if got != tc.want {
				t.Fatalf("visibleWidth(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestPadRight(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"shorter than width", "hi", 5, "hi   "},
		{"equal to width", "abc", 3, "abc"},
		{"longer than width — unchanged", "abcdef", 3, "abcdef"},
		{"ignores ansi when measuring", "\x1b[31mhi\x1b[0m", 5, "\x1b[31mhi\x1b[0m   "},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := padRight(tc.in, tc.width)
			if got != tc.want {
				t.Fatalf("padRight(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
			}
		})
	}
}
