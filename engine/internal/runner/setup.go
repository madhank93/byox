package runner

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/madhank93/byox/course"
)

// Setup checks out course + tester repos at their pinned commits, builds
// testers, and seeds Go starters into solutions/. Idempotent; never
// overwrites solutions.
func Setup(root string, reg *course.Registry, log io.Writer) error {
	for _, c := range reg.Entries {
		fmt.Fprintf(log, "==> %s\n", c.Slug)
		if _, err := checkout(c.Repo, c.Ref, c.VendorDir(root), log); err != nil {
			return err
		}
		moved, err := checkout(c.TesterRepo, c.TesterRef, c.TesterDir(root), log)
		if err != nil {
			return err
		}
		if moved {
			// A tester built from the previous commit would grade against old rules.
			if err := os.Remove(c.TesterBin(root)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := buildTester(c, root, log); err != nil {
			return err
		}
		if err := seedSolution(c, root, log); err != nil {
			return err
		}
	}
	fmt.Fprintln(log, "setup complete")
	return nil
}

// checkout makes dir a shallow checkout of repo at ref, reporting whether it
// had to move. An existing checkout already at ref is left alone, offline.
func checkout(repo, ref, dir string, log io.Writer) (bool, error) {
	if head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output(); err == nil &&
		strings.TrimSpace(string(head)) == ref {
		fmt.Fprintf(log, "    %s at %.10s\n", filepath.Base(dir), ref)
		return false, nil
	}
	fmt.Fprintf(log, "    fetching %s at %.10s\n", repo, ref)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		if err := git(log, "", "init", "-q", dir); err != nil {
			return false, err
		}
		if err := git(log, dir, "remote", "add", "origin", repo); err != nil {
			return false, err
		}
	} else if err := git(log, dir, "remote", "set-url", "origin", repo); err != nil {
		return false, err
	}
	if err := git(log, dir, "fetch", "-q", "--depth=1", "origin", ref); err != nil {
		return false, fmt.Errorf("fetch %s at %s: %w", repo, ref, err)
	}
	if err := git(log, dir, "checkout", "-q", "-f", "--detach", "FETCH_HEAD"); err != nil {
		return false, err
	}
	return true, nil
}

func git(log io.Writer, dir string, args ...string) error {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr = log, log
	return cmd.Run()
}

// EnsureTester builds the tester binary if missing.
func EnsureTester(c course.CourseRef, root string, log io.Writer) error {
	if _, err := os.Stat(c.TesterBin(root)); err == nil {
		return nil
	}
	return buildTester(c, root, log)
}

func buildTester(c course.CourseRef, root string, log io.Writer) error {
	if _, err := os.Stat(c.TesterBin(root)); err == nil {
		fmt.Fprintf(log, "    tester already built\n")
		return nil
	}
	dir := c.TesterDir(root)
	fmt.Fprintf(log, "    building tester %s\n", filepath.Base(dir))
	cmd := exec.Command("go", "build", "-o", c.TesterBin(root), "./cmd/tester")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=auto")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build tester in %s: %w", dir, err)
	}
	return nil
}

func seedSolution(c course.CourseRef, root string, log io.Writer) error {
	dst := c.SolutionDir(root)
	if _, err := os.Stat(dst); err == nil {
		fmt.Fprintf(log, "    solution exists, not touching it\n")
		return nil
	}
	src := filepath.Join(c.VendorDir(root), "compiled_starters", "go")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("no Go starter at %s: %w", src, err)
	}
	fmt.Fprintf(log, "    seeding solution from compiled_starters/go\n")
	if err := copyTree(src, dst); err != nil {
		return fmt.Errorf("seed %s: %w", dst, err)
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}
