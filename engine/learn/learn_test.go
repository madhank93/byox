package learn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStripFrontmatter(t *testing.T) {
	cases := []struct{ in, want string }{
		{"---\ntitle: x\n---\n\nbody\n", "\nbody\n"},
		{"no frontmatter\n", "no frontmatter\n"},
		{"---\nunterminated\n", "---\nunterminated\n"},
	}
	for _, c := range cases {
		if got := stripFrontmatter(c.in); got != c.want {
			t.Errorf("stripFrontmatter(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNoteAndPrimer(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, Dir, "http-server")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.md", "---\ntitle: primer\n---\n\nthe primer\n")
	write("07-ap6.md", "---\ntitle: note\n---\n\nthe note\n")

	if got, ok := Primer(root, "http-server"); !ok || got != "the primer" {
		t.Errorf("Primer = %q, %v", got, ok)
	}
	if got, ok := Note(root, "http-server", 7, "ap6"); !ok || got != "the note" {
		t.Errorf("Note = %q, %v", got, ok)
	}
	if _, ok := Note(root, "http-server", 8, "qv8"); ok {
		t.Error("Note for an unwritten stage should report missing, not empty")
	}
	if _, ok := Primer(root, "redis"); ok {
		t.Error("Primer for a course with no content should report missing")
	}
}
