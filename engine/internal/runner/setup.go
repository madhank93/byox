package runner

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/madhan/byox/internal/course"
)

// Setup clones/updates course + tester repos, builds testers, and seeds
// Go starters into solutions/. Idempotent; never overwrites solutions.
func Setup(root string, reg *course.Registry, log io.Writer) error {
	for _, c := range reg.Entries {
		fmt.Fprintf(log, "==> %s\n", c.Slug)
		if err := cloneOrPull(c.Repo, c.VendorDir(root), log); err != nil {
			return err
		}
		if err := cloneOrPull(c.TesterRepo, c.TesterDir(root), log); err != nil {
			return err
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

func cloneOrPull(repo, dir string, log io.Writer) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		fmt.Fprintf(log, "    %s already cloned\n", filepath.Base(dir))
		return nil
	}
	fmt.Fprintf(log, "    cloning %s\n", repo)
	cmd := exec.Command("git", "clone", "--depth=1", repo, dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clone %s: %w", repo, err)
	}
	return nil
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
