// Package learn loads byox's own teaching content: the course primers and
// per-stage concept notes under learn/. This is the only stage content the
// repo owns — CodeCrafters' vendored stage_descriptions/ are theirs and are
// never committed — so coverage is partial by design and a missing file is
// an ordinary outcome, not an error.
package learn

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Dir is the repo-relative root of the teaching content.
const Dir = "learn"

// StageDir is the shared identifier for a stage across the repo: the same
// NN-slug used by reference-solutions/<course>/ directories.
func StageDir(index int, slug string) string {
	return fmt.Sprintf("%02d-%s", index, slug)
}

// Primer returns the course primer's body, without frontmatter.
func Primer(root, course string) (string, bool) {
	return read(filepath.Join(root, Dir, course, "index.md"))
}

// Note returns a stage's concept note body, without frontmatter. index is
// 1-based and slug is the CodeCrafters stage slug.
func Note(root, course string, index int, slug string) (string, bool) {
	return read(filepath.Join(root, Dir, course, StageDir(index, slug)+".md"))
}

func read(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	body := strings.TrimSpace(stripFrontmatter(string(data)))
	return body, body != ""
}

// stripFrontmatter drops a leading YAML block delimited by --- lines. The
// frontmatter carries authoring metadata (title, concepts) that both
// surfaces already know from the catalog, so it never reaches the reader.
func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	if end := strings.Index(s[4:], "\n---"); end >= 0 {
		rest := s[4+end+len("\n---"):]
		return strings.TrimPrefix(rest, "\n")
	}
	return s
}

var detailsBlock = regexp.MustCompile(`(?s)<details>\s*<summary>(.*?)</summary>(.*?)</details>`)

// SplitHints separates a note's hint ladder from the rest of it. The web
// renders the hints as real <details> elements the reader chooses to open;
// a terminal has no such control, so the TUI keeps them back until asked.
// Returns the note without its Hints section, and the hints rendered as
// plain markdown (empty if the note has none).
func SplitHints(note string) (body, hints string) {
	i := strings.Index(note, "\n## Hints\n")
	if i < 0 {
		return note, ""
	}
	rest := note[i+len("\n## Hints\n"):]
	end := strings.Index(rest, "\n## ")
	if end < 0 {
		end = len(rest)
	}
	body = strings.TrimSpace(note[:i]) + "\n\n" + strings.TrimSpace(rest[end:])
	hints = detailsBlock.ReplaceAllString(strings.TrimSpace(rest[:end]), "**$1**\n$2")
	return strings.TrimSpace(body), strings.TrimSpace(hints)
}
