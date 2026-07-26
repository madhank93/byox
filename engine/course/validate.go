package course

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// slugRe is the shape every course slug must have. Slugs become path elements
// under solutions/, so they are constrained to what is unambiguously safe as a
// single directory name.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// scpLikeRe matches git's scp-style remote syntax, "user@host:path".
var scpLikeRe = regexp.MustCompile(`^[^/@]+@[^/:]+:`)

// validateSlug rejects a slug that could escape the directory it names.
//
// courses.yml is repo-owned, not user input, but every slug in it is joined
// into solutions/<slug> and testers/<base>, and a slug of "../../etc" would
// have been joined without complaint. Validating once at load keeps every
// consumer of CourseRef safe and fails fast with a clear message rather than
// at some later filepath.Join.
func validateSlug(s string) error {
	if s == "" {
		return fmt.Errorf("course slug is empty")
	}
	if !slugRe.MatchString(s) {
		return fmt.Errorf("course slug %q must match %s", s, slugRe)
	}
	return nil
}

// validateRepo rejects a repo URL whose last path element would not be a safe
// directory name. repoBase takes that element verbatim to name the clone
// directory, so ".." or an absolute-looking element would place the checkout
// outside vendor/ or testers/.
func validateRepo(field, raw string) error {
	if raw == "" {
		return fmt.Errorf("%s is empty", field)
	}
	// scp-style "git@host:org/repo" has no scheme and does not survive
	// url.Parse cleanly, so recognise it before parsing.
	if !scpLikeRe.MatchString(raw) {
		u, err := url.Parse(raw)
		if err != nil {
			return fmt.Errorf("%s %q: %w", field, raw, err)
		}
		switch u.Scheme {
		case "https", "http", "ssh", "git":
		default:
			return fmt.Errorf("%s %q: unsupported scheme %q", field, raw, u.Scheme)
		}
	}
	base := repoBase(raw)
	if base == "" || base == "." || base == ".." || strings.ContainsRune(base, '/') ||
		strings.ContainsRune(base, '\\') || base != path.Clean(base) {
		return fmt.Errorf("%s %q: last path element %q is not a usable directory name", field, raw, base)
	}
	return nil
}

// Validate checks a single registry entry.
func (c CourseRef) Validate() error {
	if err := validateSlug(c.Slug); err != nil {
		return err
	}
	if err := validateRepo("repo", c.Repo); err != nil {
		return fmt.Errorf("course %s: %w", c.Slug, err)
	}
	if err := validateRepo("tester_repo", c.TesterRepo); err != nil {
		return fmt.Errorf("course %s: %w", c.Slug, err)
	}
	return nil
}
