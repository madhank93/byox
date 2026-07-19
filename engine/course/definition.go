package course

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Stage is one stage of a vendored course-definition.yml. Parsed
// defensively: only slug/name/difficulty are relied upon.
type Stage struct {
	Slug          string `yaml:"slug"`
	Name          string `yaml:"name"`
	Difficulty    string `yaml:"difficulty"`
	DescriptionMD string `yaml:"description_md"`
}

type Definition struct {
	Slug          string  `yaml:"slug"`
	Name          string  `yaml:"name"`
	DescriptionMD string  `yaml:"description_md"`
	Stages        []Stage `yaml:"stages"`
}

// LoadDefinition parses vendor/<course>/course-definition.yml.
func LoadDefinition(vendorDir string) (*Definition, error) {
	path := filepath.Join(vendorDir, "course-definition.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s (run `just setup` first?): %w", path, err)
	}
	var d Definition
	if err := yaml.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(d.Stages) == 0 {
		return nil, fmt.Errorf("%s: no stages found", path)
	}
	return &d, nil
}

// StageDescription returns the instructions markdown for a stage: the
// stage_descriptions/ file matching the slug, else the inline
// description_md field, else a stub.
func (d *Definition) StageDescription(vendorDir string, s Stage) string {
	dir := filepath.Join(vendorDir, "stage_descriptions")
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			name := e.Name()
			base := strings.TrimSuffix(name, filepath.Ext(name))
			// Files are named like "<slug>.md" or "NN-<slug>.md".
			if base == s.Slug || strings.HasSuffix(base, "-"+s.Slug) {
				if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
					return resolveTemplate(string(data), "go")
				}
			}
		}
	}
	if s.DescriptionMD != "" {
		return resolveTemplate(s.DescriptionMD, "go")
	}
	return fmt.Sprintf("# %s\n\n_No description found for stage `%s`._", s.Name, s.Slug)
}

// resolveTemplate evaluates the Mustache-style conditionals CodeCrafters
// stage descriptions use — {{#name}}…{{/name}} (kept when name resolves
// true) and {{^name}}…{{/name}} (kept when it resolves false) — then
// strips any leftover tags. Two tag families are known: "lang_is_X" (kept
// only for the given lang) and "reader_is_bot" (extra clarifying detail
// CodeCrafters shows an AI/agent reader; always kept here, since it's
// genuinely useful context and this tool has no human/bot reader
// distinction to make). Any other tag name defaults to true — an unknown
// future flag should fail open (show the content) rather than silently
// hide instructions a reader might need.
func resolveTemplate(md, lang string) string {
	for {
		open := findTag(md)
		if open < 0 {
			break
		}
		sigil, name, tagEnd := parseTag(md, open)
		if name == "" {
			break // malformed; stop to avoid a loop
		}
		closeTag := "{{/" + name + "}}"
		closeIdx := strings.Index(md[tagEnd:], closeTag)
		if closeIdx < 0 {
			break
		}
		body := md[tagEnd : tagEnd+closeIdx]
		truthy := tagTruthy(name, lang)
		keep := (sigil == '#' && truthy) || (sigil == '^' && !truthy)
		rest := md[tagEnd+closeIdx+len(closeTag):]
		if keep {
			md = md[:open] + body + rest
		} else {
			md = md[:open] + rest
		}
	}
	return md
}

// tagTruthy resolves a conditional tag's name to true/false for this
// reader (see resolveTemplate's doc comment for the known tag families).
func tagTruthy(name, lang string) bool {
	if langName, ok := strings.CutPrefix(name, "lang_is_"); ok {
		return langName == lang
	}
	return true
}

// findTag returns the index of the next {{#…}} or {{^…}} tag.
func findTag(md string) int {
	a := strings.Index(md, "{{#")
	b := strings.Index(md, "{{^")
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	default:
		return min(a, b)
	}
}

// parseTag reads the tag at idx, returning its sigil (#/^), tag name, and
// the index just past the closing }}.
func parseTag(md string, idx int) (byte, string, int) {
	sigil := md[idx+2] // char after "{{"
	nameStart := idx + 3
	end := strings.Index(md[nameStart:], "}}")
	if end < 0 {
		return sigil, "", idx
	}
	return sigil, md[nameStart : nameStart+end], nameStart + end + 2
}
