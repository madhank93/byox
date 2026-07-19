// Package diff computes a compact, unified-diff-style line delta between
// two text files. It backs the "changes for this stage" view in both the
// TUI (s-key) and the generated website, so the two surfaces stay in sync:
// each shows only what a stage's reference solution added, not the whole
// cumulative program.
package diff

import (
	"fmt"
	"strings"
)

// Op is one line of a line-level edit script: ' ' unchanged, '-' present
// only in old, '+' present only in new.
type Op struct {
	Kind byte
	Text string
}

// Context is how many unchanged lines to keep around each change when
// formatting, matching the readability of a typical unified diff.
const Context = 2

// Unified is the convenience entry point: it returns a compact
// unified-diff-style rendering of the change from old to new, and whether
// the two differ at all.
func Unified(old, new string) (out string, changed bool) {
	if old == new {
		return "", false
	}
	return Format(Lines(strings.Split(old, "\n"), strings.Split(new, "\n"))), true
}

// Lines computes a minimal line-level edit script from old to new via a
// standard LCS dynamic program. Reference solutions here top out around a
// couple thousand lines, so the O(n·m) table is cheap enough to build on
// demand.
func Lines(old, new []string) []Op {
	n, m := len(old), len(new)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if old[i] == new[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops []Op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case old[i] == new[j]:
			ops = append(ops, Op{' ', old[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, Op{'-', old[i]})
			i++
		default:
			ops = append(ops, Op{'+', new[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, Op{'-', old[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, Op{'+', new[j]})
	}
	return ops
}

// Format renders an edit script as unified-diff-style text (a leading
// "+"/"-"/" " per line, no hunk headers), collapsing long unchanged runs
// down to a short elision marker so the output stays focused on what
// actually changed.
func Format(ops []Op) string {
	var out []string
	n := len(ops)
	for i := 0; i < n; {
		if ops[i].Kind != ' ' {
			out = append(out, string(ops[i].Kind)+ops[i].Text)
			i++
			continue
		}
		j := i
		for j < n && ops[j].Kind == ' ' {
			j++
		}
		runLen := j - i
		atStart, atEnd := i == 0, j == n
		switch {
		case atStart && atEnd:
			for k := i; k < j; k++ {
				out = append(out, " "+ops[k].Text)
			}
		case atStart:
			skip := runLen - Context
			start := i
			if skip > 0 {
				out = append(out, fmt.Sprintf(" … %d unchanged lines …", skip))
				start = j - Context
			}
			for k := start; k < j; k++ {
				out = append(out, " "+ops[k].Text)
			}
		case atEnd:
			show := min(Context, runLen)
			for k := i; k < i+show; k++ {
				out = append(out, " "+ops[k].Text)
			}
			if runLen > show {
				out = append(out, fmt.Sprintf(" … %d unchanged lines …", runLen-show))
			}
		case runLen <= Context*2:
			for k := i; k < j; k++ {
				out = append(out, " "+ops[k].Text)
			}
		default:
			for k := i; k < i+Context; k++ {
				out = append(out, " "+ops[k].Text)
			}
			out = append(out, fmt.Sprintf(" … %d unchanged lines …", runLen-Context*2))
			for k := j - Context; k < j; k++ {
				out = append(out, " "+ops[k].Text)
			}
		}
		i = j
	}
	return strings.Join(out, "\n")
}
