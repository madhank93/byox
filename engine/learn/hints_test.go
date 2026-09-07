package learn

import (
	"strings"
	"testing"
)

const note = `## Core concept

The idea.

## Hints

<details><summary>Nudge</summary>

Try the small thing.
</details>

## Further reading

- [a link](https://example.com)
`

func TestSplitHints(t *testing.T) {
	body, hints := SplitHints(note)

	for _, want := range []string{"## Core concept", "## Further reading"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lost %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Try the small thing") {
		t.Errorf("body still carries the hint text:\n%s", body)
	}
	if !strings.Contains(hints, "**Nudge**") || !strings.Contains(hints, "Try the small thing") {
		t.Errorf("hints = %q", hints)
	}
	if strings.Contains(hints, "<details>") {
		t.Errorf("hints still carry HTML the terminal can't collapse: %q", hints)
	}
}

func TestSplitHintsWithoutHints(t *testing.T) {
	in := "## Core concept\n\nJust prose.\n"
	body, hints := SplitHints(in)
	if body != in || hints != "" {
		t.Errorf("SplitHints(no hints) = %q, %q", body, hints)
	}
}
