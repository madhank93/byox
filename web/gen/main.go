// Command gen renders the byox catalog data (catalog.ts + per-stage detail
// markdown) from courses.yml, each course's vendored course-definition.yml,
// and the tester-verified snapshots under reference-solutions/. It's its
// own Go module (so it doesn't drag the TUI's dependency tree into the
// generator, and vice versa) — run it from inside web/gen: `go run .`,
// or from the repo root via `just gen`.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/madhan/byox/course"
)

const (
	dataDir    = "web/src/data"
	detailsDir = "web/src/data/stage-details"
)

// courseColor gives every stage of a course the same chip color on
// /catalog, one per courses.yml entry in registry order.
var courseColors = []string{
	"#c53030", "#7c6af5", "#4fa86d", "#3b9eff", "#d29922",
	"#1f8a9c", "#e36f0e", "#9b5de5", "#e85d9f", "#00add8",
}

// courseNotes carries the one piece of per-course status this generator
// can't derive from the tester-verified snapshot count alone: why a
// course isn't (yet) fully verified. Absent entries just show
// "N/M stages verified".
var courseNotes = map[string]string{
	"bittorrent": "Stages 1-9 are tester-verified. Stage 10 onward need the real bittorrent-test-tracker.codecrafters.io and live seeded peers, not a local mock — blocked in the sandboxed environment these solutions were authored in. See the repo's PR #6 for the full writeup.",
}

type stageEntry struct {
	Course      string
	CourseName  string
	Index       int
	Slug        string
	Title       string
	Difficulty  string
	Description string
	Verified    bool
	SourcePath  string // repo-relative path to the snapshotted main.go, if verified
}

type courseInfo struct {
	Slug     string
	Name     string
	Repo     string
	Color    string
	Learn    string
	Total    int
	Verified int
	Note     string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := course.FindRoot()
	if err != nil {
		return err
	}
	reg, err := course.LoadRegistry(root)
	if err != nil {
		return err
	}

	// Regenerated wholesale every run — clear stale details from
	// renamed/removed stages before the loop below repopulates it.
	if err := os.RemoveAll(filepath.Join(root, detailsDir)); err != nil {
		return err
	}

	var stages []stageEntry
	var courses []courseInfo

	for i, c := range reg.Entries {
		vendorDir := c.VendorDir(root)
		if err := ensureVendored(vendorDir, c.Repo); err != nil {
			return fmt.Errorf("%s: %w", c.Slug, err)
		}
		def, err := course.LoadDefinition(vendorDir)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Slug, err)
		}

		verifiedByIndex := verifiedStages(filepath.Join(root, "reference-solutions", c.Slug))

		ci := courseInfo{
			Slug:  c.Slug,
			Name:  c.Name,
			Repo:  c.Repo,
			Color: courseColors[i%len(courseColors)],
			Learn: plainText(def.DescriptionMD),
			Total: len(def.Stages),
			Note:  courseNotes[c.Slug],
		}

		for idx, s := range def.Stages {
			n := idx + 1
			snapDir, ok := verifiedByIndex[n]
			e := stageEntry{
				Course:      c.Slug,
				CourseName:  c.Name,
				Index:       n,
				Slug:        s.Slug,
				Title:       s.Name,
				Difficulty:  s.Difficulty,
				Description: plainText(firstParagraph(def.StageDescription(vendorDir, s))),
				Verified:    ok,
			}
			if ok {
				ci.Verified++
				e.SourcePath = fmt.Sprintf("reference-solutions/%s/%s/main.go", c.Slug, snapDir)
			}
			// Every stage gets a detail file (full description); only
			// verified ones get the reference-solution spoiler appended.
			if err := writeDetail(root, e, def.StageDescription(vendorDir, s)); err != nil {
				return fmt.Errorf("%s/%s: %w", c.Slug, s.Slug, err)
			}
			stages = append(stages, e)
		}
		courses = append(courses, ci)
	}

	return writeCatalog(root, courses, stages)
}

// ensureVendored shallow-clones repo into dir if course-definition.yml
// isn't already there (mirrors what `just setup` does for the runner
// itself, so this generator works from a fresh checkout with no prior
// `just setup` run, e.g. in CI).
func ensureVendored(dir, repo string) error {
	if _, err := os.Stat(filepath.Join(dir, "course-definition.yml")); err == nil {
		return nil
	}
	fmt.Fprintf(os.Stderr, "gen: cloning %s -> %s\n", repo, dir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	cmd := exec.Command("git", "clone", "--depth", "1", repo, dir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	// The kafka course repo checks in a >100MB LFS asset (a real Kafka
	// install, used only to build fixtures for the tester's own test
	// suite) that most GitHub tokens can't fetch. This generator only
	// needs course-definition.yml and stage_descriptions/, so skip the
	// smudge entirely rather than fail the whole clone over it.
	cmd.Env = append(os.Environ(), "GIT_LFS_SKIP_SMUDGE=1")
	return cmd.Run()
}

// stageDirRe matches a reference-solutions/<course> snapshot directory
// name: a stage number (any zero-padding), a dash, then the stage slug.
var stageDirRe = regexp.MustCompile(`^0*(\d+)-(.+)$`)

// verifiedStages maps a course's tester-verified stage numbers to their
// snapshot directory name (e.g. 67 -> "67-de8"). A missing entry means
// that stage has no tester-verified snapshot yet.
func verifiedStages(courseDir string) map[int]string {
	out := map[int]string{}
	entries, err := os.ReadDir(courseDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := stageDirRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		out[n] = e.Name()
	}
	return out
}

var (
	mdLink    = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	mdCode    = regexp.MustCompile("`([^`]+)`")
	mdHeading = regexp.MustCompile(`(?m)^#{1,6}\s+`)
	mdBold    = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

// plainText strips the light markdown CodeCrafters descriptions use
// (headings, links, code spans, bold) down to prose, for the catalog
// table's one-line description and the course "learn" popover.
func plainText(s string) string {
	s = mdHeading.ReplaceAllString(s, "")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdCode.ReplaceAllString(s, "$1")
	s = mdBold.ReplaceAllString(s, "$1")
	return strings.TrimSpace(strings.Join(strings.Fields(s), " "))
}

// firstParagraph returns the first non-empty, non-heading paragraph of a
// stage description — the one-sentence summary shown in the catalog row.
func firstParagraph(md string) string {
	for _, para := range strings.Split(md, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" || strings.HasPrefix(para, "#") {
			continue
		}
		return para
	}
	return ""
}

// writeDetail renders one stage's modal content: the full instructions,
// then the tester-verified reference solution's source behind a
// <details> spoiler so it never loads with the table.
func writeDetail(root string, e stageEntry, fullDescription string) error {
	dir := filepath.Join(root, detailsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var b strings.Builder
	// YAML-quote the title: JSON string syntax (used here via js()) is a
	// valid YAML double-quoted scalar, so this survives titles with a
	// colon, quotes, or other characters that break an unquoted scalar
	// (e.g. "Variables: Initialize variables").
	fmt.Fprintf(&b, "---\ntitle: %s\n---\n\n", js(e.Title))
	b.WriteString(fullDescription)
	b.WriteString("\n\n")

	src, err := os.ReadFile(filepath.Join(root, e.SourcePath))
	if err == nil {
		b.WriteString("<details>\n<summary>Show the reference solution (spoiler)</summary>\n\n")
		fmt.Fprintf(&b, "```go title=%q\n%s\n```\n\n", "main.go", strings.TrimRight(string(src), "\n"))
		b.WriteString("</details>\n")
	}

	name := e.Course + "-" + e.Slug + ".md"
	return os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644)
}

// writeCatalog emits src/data/catalog.ts: the typed COURSES/CATALOG data
// the /catalog page renders at build time. Per-stage detail files were
// already written by writeDetail during run()'s course loop.
func writeCatalog(root string, courses []courseInfo, stages []stageEntry) error {
	var b strings.Builder
	b.WriteString("// AUTO-GENERATED by `go run ./web/gen` from courses.yml — do not hand-edit.\n\n")
	b.WriteString("export type CatalogEntry = {\n")
	b.WriteString("  course: string;\n  index: number;\n  slug: string;\n  title: string;\n")
	b.WriteString("  difficulty: string;\n  description: string;\n  verified: boolean;\n  sourcePath: string;\n};\n\n")

	b.WriteString("export const COURSES: Record<string, { name: string; repo: string; color: string; learn: string; total: number; verified: number; note: string }> = {\n")
	for _, c := range courses {
		fmt.Fprintf(&b, "  %s: { name: %s, repo: %s, color: %s, learn: %s, total: %d, verified: %d, note: %s },\n",
			js(c.Slug), js(c.Name), js(c.Repo), js(c.Color), js(c.Learn), c.Total, c.Verified, js(c.Note))
	}
	b.WriteString("};\n\n")

	b.WriteString("export const CATALOG: CatalogEntry[] = [\n")
	for _, e := range stages {
		fmt.Fprintf(&b, "  { course: %s, index: %d, slug: %s, title: %s, difficulty: %s, description: %s, verified: %s, sourcePath: %s },\n",
			js(e.Course), e.Index, js(e.Slug), js(e.Title), js(e.Difficulty), js(e.Description), boolLit(e.Verified), js(e.SourcePath))
	}
	b.WriteString("];\n")

	if err := os.MkdirAll(filepath.Join(root, dataDir), 0o755); err != nil {
		return err
	}
	out := filepath.Join(root, dataDir, "catalog.ts")
	if err := os.WriteFile(out, []byte(b.String()), 0o644); err != nil {
		return err
	}

	total, verified := 0, 0
	for _, c := range courses {
		total += c.Total
		verified += c.Verified
	}
	fmt.Fprintf(os.Stderr, "gen: wrote %d courses, %d/%d stages verified -> %s\n", len(courses), verified, total, out)
	return nil
}

func js(s string) string {
	out, _ := json.Marshal(s)
	return string(out)
}

func boolLit(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
