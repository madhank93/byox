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

// resolveTemplate evaluates the Mustache-style language conditionals that
// CodeCrafters stage descriptions use — {{#lang_is_go}}…{{/lang_is_go}}
// (kept when lang matches) and {{^lang_is_go}}…{{/lang_is_go}} (kept when
// it doesn't) — for the given language, then strips any leftover tags.
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
		closeTag := "{{/lang_is_" + name + "}}"
		closeIdx := strings.Index(md[tagEnd:], closeTag)
		if closeIdx < 0 {
			break
		}
		body := md[tagEnd : tagEnd+closeIdx]
		keep := (sigil == '#' && name == lang) || (sigil == '^' && name != lang)
		rest := md[tagEnd+closeIdx+len(closeTag):]
		if keep {
			md = md[:open] + body + rest
		} else {
			md = md[:open] + rest
		}
	}
	return md
}

// findTag returns the index of the next {{#lang_is_ or {{^lang_is_ tag.
func findTag(md string) int {
	a := strings.Index(md, "{{#lang_is_")
	b := strings.Index(md, "{{^lang_is_")
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	default:
		return min(a, b)
	}
}

// parseTag reads the tag at idx, returning its sigil (#/^), language
// name, and the index just past the closing }}.
func parseTag(md string, idx int) (byte, string, int) {
	sigil := md[idx+2] // char after "{{"
	nameStart := idx + len("{{#lang_is_")
	end := strings.Index(md[nameStart:], "}}")
	if end < 0 {
		return sigil, "", idx
	}
	return sigil, md[nameStart : nameStart+end], nameStart + end + 2
}
