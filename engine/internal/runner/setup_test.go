package runner

import (
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckoutFollowsThePin(t *testing.T) {
	src := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(src, "init", "-q")
	run(src, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "one")
	first := run(src, "rev-parse", "HEAD")
	run(src, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "two")
	second := run(src, "rev-parse", "HEAD")

	dst := filepath.Join(t.TempDir(), "clone")
	for _, step := range []struct {
		ref  string
		move bool
	}{{first, true}, {first, false}, {second, true}} {
		moved, err := checkout(src, step.ref, dst, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if moved != step.move {
			t.Errorf("checkout %.7s: moved = %v, want %v", step.ref, moved, step.move)
		}
		if head := run(dst, "rev-parse", "HEAD"); head != step.ref {
			t.Errorf("HEAD = %.7s, want %.7s", head, step.ref)
		}
	}
}
