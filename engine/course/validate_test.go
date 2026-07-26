package course

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSlug(t *testing.T) {
	ok := []string{"redis", "dns-server", "http-server", "grep", "sqlite3"}
	for _, s := range ok {
		if err := validateSlug(s); err != nil {
			t.Errorf("validateSlug(%q) = %v, want nil", s, err)
		}
	}
	bad := []string{"", "..", "../etc", "a/b", "/abs", "Redis", "-leading", "with space", "dot.dot"}
	for _, s := range bad {
		if err := validateSlug(s); err == nil {
			t.Errorf("validateSlug(%q) = nil, want an error", s)
		}
	}
}

func TestValidateRepo(t *testing.T) {
	ok := []string{
		"https://github.com/codecrafters-io/build-your-own-redis",
		"https://github.com/codecrafters-io/redis-tester.git",
		"git@github.com:codecrafters-io/redis-tester.git",
	}
	for _, r := range ok {
		if err := validateRepo("repo", r); err != nil {
			t.Errorf("validateRepo(%q) = %v, want nil", r, err)
		}
	}
	bad := []string{
		"",
		"https://example.com/a/..",
		"https://example.com/a/../..",
		"file:///etc/passwd",
	}
	for _, r := range bad {
		if err := validateRepo("repo", r); err == nil {
			t.Errorf("validateRepo(%q) = nil, want an error", r)
		}
	}
}

// A traversing slug used to be joined into solutions/<slug> unchallenged.
func TestLoadRegistryRejectsTraversingSlug(t *testing.T) {
	dir := t.TempDir()
	yml := `entries:
  - slug: ../../../tmp/evil
    name: Evil
    repo: https://github.com/x/y
    tester_repo: https://github.com/x/y-tester
`
	if err := os.WriteFile(filepath.Join(dir, "courses.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRegistry(dir)
	if err == nil {
		t.Fatal("LoadRegistry accepted a slug that escapes solutions/")
	}
	if !strings.Contains(err.Error(), "slug") {
		t.Errorf("error %q does not name the offending field", err)
	}
}

func TestLoadRegistryRejectsDuplicateSlug(t *testing.T) {
	dir := t.TempDir()
	yml := `entries:
  - slug: redis
    name: One
    repo: https://github.com/x/y
    tester_repo: https://github.com/x/y-tester
  - slug: redis
    name: Two
    repo: https://github.com/x/z
    tester_repo: https://github.com/x/z-tester
`
	if err := os.WriteFile(filepath.Join(dir, "courses.yml"), []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(dir); err == nil {
		t.Fatal("LoadRegistry accepted two courses with the same slug")
	}
}

// The real registry must keep passing its own rules.
func TestRepoRegistryIsValid(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "courses.yml")); err != nil {
		t.Skipf("no courses.yml at %s", root)
	}
	if _, err := LoadRegistry(root); err != nil {
		t.Fatalf("the committed courses.yml does not validate: %v", err)
	}
}
