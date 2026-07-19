package course

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// CourseRef is one entry in the repo-root courses.yml registry.
type CourseRef struct {
	Slug       string `yaml:"slug"`
	Name       string `yaml:"name"`
	Repo       string `yaml:"repo"`
	TesterRepo string `yaml:"tester_repo"`
}

type Registry struct {
	Entries []CourseRef `yaml:"entries"`
}

func LoadRegistry(root string) (*Registry, error) {
	data, err := os.ReadFile(filepath.Join(root, "courses.yml"))
	if err != nil {
		return nil, fmt.Errorf("read courses.yml: %w", err)
	}
	var r Registry
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse courses.yml: %w", err)
	}
	if len(r.Entries) == 0 {
		return nil, fmt.Errorf("courses.yml has no entries")
	}
	return &r, nil
}

func (r *Registry) Find(slug string) (CourseRef, error) {
	for _, c := range r.Entries {
		if c.Slug == slug {
			return c, nil
		}
	}
	return CourseRef{}, fmt.Errorf("unknown course %q (see courses.yml)", slug)
}

func repoBase(url string) string {
	return strings.TrimSuffix(filepath.Base(url), ".git")
}

func (c CourseRef) VendorDir(root string) string {
	return filepath.Join(root, "vendor", repoBase(c.Repo))
}

func (c CourseRef) TesterDir(root string) string {
	return filepath.Join(root, "testers", repoBase(c.TesterRepo))
}

func (c CourseRef) TesterBin(root string) string {
	return filepath.Join(c.TesterDir(root), "dist", "main.out")
}

func (c CourseRef) SolutionDir(root string) string {
	return filepath.Join(root, "solutions", c.Slug)
}

// FindRoot walks upward from cwd looking for courses.yml.
func FindRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "courses.yml")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("courses.yml not found in any parent of the working directory")
		}
		dir = parent
	}
}
