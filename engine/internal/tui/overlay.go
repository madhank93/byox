package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// overlay composites box on top of base at (x, y), returning a block with
// base's exact dimensions.
//
// Bubble Tea has no window system: View returns one string, and anything that
// looks like a pop-up is this — cut a hole in the rendered frame and paste. The
// cutting has to be ANSI-aware, because a line is a mix of printable cells and
// escape sequences that occupy none, and slicing on byte offsets tears a colour
// sequence in half and bleeds it across the rest of the screen.
//
// Keeping base's dimensions is the contract: a composite one line taller than
// the terminal wraps, and a wrapped frame scrolls the header away.
func overlay(base, box string, x, y int) string {
	baseLines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")
	if len(boxLines) == 0 {
		return base
	}
	baseW := lipgloss.Width(base)
	x = max(x, 0)
	y = max(y, 0)
	boxW := min(lipgloss.Width(box), max(baseW-x, 0))
	if boxW == 0 {
		return base
	}

	for i, line := range boxLines {
		row := y + i
		if row < 0 || row >= len(baseLines) {
			continue
		}
		under := baseLines[row]
		underW := lipgloss.Width(under)

		left := ansi.Truncate(under, x, "")
		if w := lipgloss.Width(left); w < x {
			left += strings.Repeat(" ", x-w)
		}
		var right string
		if underW > x+boxW {
			right = ansi.TruncateLeft(under, x+boxW, "")
		}

		switch w := lipgloss.Width(line); {
		case w < boxW:
			line += strings.Repeat(" ", boxW-w)
		case w > boxW:
			line = ansi.Truncate(line, boxW, "")
		}
		baseLines[row] = left + line + right
	}
	return strings.Join(baseLines, "\n")
}

// center returns the top-left corner that puts a box of the given size in the
// middle of w×h, clamped so it never starts off-screen.
func center(w, h, boxW, boxH int) (int, int) {
	return max((w-boxW)/2, 0), max((h-boxH)/2, 0)
}

// modal renders a titled box for overlaying: a prompt, its body, and the keys
// that answer it.
func modal(title, body, keys string, width int) string {
	inner := max(min(width-6, 72), 24)
	content := lipgloss.NewStyle().Bold(true).Render(title)
	if body != "" {
		content += "\n\n" + lipgloss.NewStyle().Width(inner).Render(body)
	}
	if keys != "" {
		content += "\n\n" + keys
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("214")).
		Padding(0, 2).
		Width(inner).
		MaxWidth(inner + 6).
		Render(content)
}
