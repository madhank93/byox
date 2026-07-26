package tui

import (
	"fmt"
	"strings"
)

// maxLogLines bounds how much tester output the log pane retains.
//
// The log was an unbounded strings.Builder, re-stringified into the viewport
// on every line that arrived. A tester that emits a lot of output — a failing
// stage replaying every command, a program stuck in a print loop — therefore
// cost quadratic time and unbounded memory to display.
//
// The tail is what matters: a tester reports the failure it stopped on last.
const maxLogLines = 5000

// logBuffer keeps the most recent maxLogLines lines of a run, and caches the
// joined form so repeated renders are free.
type logBuffer struct {
	lines   []string
	dropped int
	cache   string
	valid   bool
}

func (b *logBuffer) Reset() {
	b.lines = b.lines[:0]
	b.dropped = 0
	b.cache = ""
	b.valid = false
}

func (b *logBuffer) AddLine(s string) {
	b.lines = append(b.lines, s)
	if len(b.lines) > maxLogLines {
		drop := len(b.lines) - maxLogLines
		b.lines = append(b.lines[:0], b.lines[drop:]...)
		b.dropped += drop
	}
	b.valid = false
}

func (b *logBuffer) String() string {
	if b.valid {
		return b.cache
	}
	var sb strings.Builder
	if b.dropped > 0 {
		fmt.Fprintf(&sb, "… %d earlier lines dropped …\n", b.dropped)
	}
	for _, l := range b.lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	b.cache = sb.String()
	b.valid = true
	return b.cache
}
