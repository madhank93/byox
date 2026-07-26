package tui

import (
	"fmt"
	"strings"
	"testing"
)

func TestLogBufferKeepsEverythingBelowTheCap(t *testing.T) {
	var b logBuffer
	b.AddLine("first")
	b.AddLine("second")
	if got, want := b.String(), "first\nsecond\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The tail is what matters: a tester reports the failure it stopped on last.
func TestLogBufferDropsOldestPastTheCap(t *testing.T) {
	var b logBuffer
	total := maxLogLines + 500
	for i := range total {
		b.AddLine(fmt.Sprintf("line-%d", i))
	}
	out := b.String()

	if !strings.Contains(out, fmt.Sprintf("line-%d", total-1)) {
		t.Error("the most recent line was dropped")
	}
	if strings.Contains(out, "line-0\n") {
		t.Error("the oldest line survived past the cap")
	}
	if !strings.Contains(out, "earlier lines dropped") {
		t.Error("truncation was not reported to the reader")
	}
	if n := strings.Count(out, "\n"); n > maxLogLines+1 {
		t.Errorf("retained %d lines, want at most %d plus the notice", n, maxLogLines)
	}
}

func TestLogBufferReset(t *testing.T) {
	var b logBuffer
	for i := range maxLogLines + 10 {
		b.AddLine(fmt.Sprintf("line-%d", i))
	}
	b.Reset()
	if got := b.String(); got != "" {
		t.Errorf("after Reset got %q, want empty", got)
	}
}

func TestLogBufferCachesJoinedForm(t *testing.T) {
	var b logBuffer
	b.AddLine("a")
	first := b.String()
	if second := b.String(); second != first {
		t.Error("repeated String() returned different content")
	}
	b.AddLine("b")
	if third := b.String(); third == first {
		t.Error("String() did not pick up a line added after it was cached")
	}
}
