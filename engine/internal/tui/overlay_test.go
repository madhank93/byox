package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// TestOverlayKeepsTheFrameSize is the contract: a composite one line taller or
// one column wider than the frame wraps, and a wrapped frame scrolls the top
// bar off the screen.
func TestOverlayKeepsTheFrameSize(t *testing.T) {
	base := strings.TrimRight(strings.Repeat(strings.Repeat("x", 60)+"\n", 20), "\n")
	box := modal("help", "body text", "esc close", 40)

	for _, at := range [][2]int{{0, 0}, {10, 5}, {55, 18}, {-3, -2}, {200, 200}} {
		got := overlay(base, box, at[0], at[1])
		if lines := len(strings.Split(got, "\n")); lines != 20 {
			t.Errorf("at %v: %d lines, want 20", at, lines)
		}
		for i, l := range strings.Split(got, "\n") {
			if w := lipgloss.Width(l); w > 60 {
				t.Errorf("at %v: line %d is %d wide, base is 60", at, i, w)
			}
		}
	}
}

// TestOverlayKeepsTheEdges: the frame either side of a pop-up must survive.
func TestOverlayKeepsTheEdges(t *testing.T) {
	base := strings.TrimRight(strings.Repeat("LLLLLLLLLLMMMMMMMMMMRRRRRRRRRR\n", 6), "\n")
	lines := strings.Split(overlay(base, "[box]", 10, 2), "\n")
	if !strings.Contains(lines[2], "[box]") {
		t.Errorf("box was not pasted: %q", lines[2])
	}
	if !strings.HasPrefix(lines[2], "LLLLLLLLLL") || !strings.HasSuffix(lines[2], "RRRRRRRRRR") {
		t.Errorf("the frame around the box was lost: %q", lines[2])
	}
}

func TestElapsed(t *testing.T) {
	cases := map[time.Duration]string{
		900 * time.Millisecond:         "0s",
		42 * time.Second:               "42s",
		61 * time.Second:               "1m01s",
		9*time.Minute + 30*time.Second: "9m30s",
	}
	for d, want := range cases {
		if got := elapsed(d); got != want {
			t.Errorf("elapsed(%v) = %q, want %q", d, got, want)
		}
	}
}

// TestHelpMentionsTheKeys keeps `?` honest as bindings change.
func TestHelpMentionsTheKeys(t *testing.T) {
	for _, k := range []string{"?", "q", "/", "esc"} {
		if !strings.Contains(helpText, k) {
			t.Errorf("help does not mention %q", k)
		}
	}
}
