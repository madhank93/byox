package progress

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Store is the on-disk progress.json at the repo root.
type Store struct {
	path    string
	Courses map[string]*CourseProgress `json:"courses"`
}

type CourseProgress struct {
	Completed []string `json:"completed"`
	Streak    int      `json:"streak"` // consecutive passing runs (reset on a fail)
}

func Load(root string) (*Store, error) {
	s := &Store{
		path:    filepath.Join(root, "progress.json"),
		Courses: map[string]*CourseProgress{},
	}
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	if s.Courses == nil {
		s.Courses = map[string]*CourseProgress{}
	}
	return s, nil
}

func (s *Store) Save() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o644)
}

func (s *Store) course(slug string) *CourseProgress {
	if s.Courses[slug] == nil {
		s.Courses[slug] = &CourseProgress{}
	}
	return s.Courses[slug]
}

func (s *Store) IsCompleted(course, stage string) bool {
	return slices.Contains(s.course(course).Completed, stage)
}

func (s *Store) Streak(course string) int { return s.course(course).Streak }

// RecordRun updates the pass/fail streak for a course.
func (s *Store) RecordRun(course string, passed bool) {
	c := s.course(course)
	if passed {
		c.Streak++
	} else {
		c.Streak = 0
	}
}

func (s *Store) MarkCompleted(course, stage string) {
	if !s.IsCompleted(course, stage) {
		c := s.course(course)
		c.Completed = append(c.Completed, stage)
	}
}

// CurrentIndex returns the index of the first stage (in course order)
// not yet completed. Equals len(stages) when the course is done.
func (s *Store) CurrentIndex(course string, stageSlugs []string) int {
	for i, slug := range stageSlugs {
		if !s.IsCompleted(course, slug) {
			return i
		}
	}
	return len(stageSlugs)
}

// ResetTo rewinds progress so that the given stage becomes current:
// every stage at or after it is marked incomplete.
func (s *Store) ResetTo(course, stage string, stageSlugs []string) error {
	idx := slices.Index(stageSlugs, stage)
	if idx < 0 {
		return fmt.Errorf("unknown stage %q", stage)
	}
	keep := stageSlugs[:idx]
	c := s.course(course)
	c.Completed = slices.DeleteFunc(c.Completed, func(done string) bool {
		return !slices.Contains(keep, done)
	})
	return nil
}
