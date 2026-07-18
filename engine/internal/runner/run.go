package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/madhan/byox/internal/course"
)

// testCase mirrors the JSON the official codecrafters testers expect in
// CODECRAFTERS_TEST_CASES_JSON.
type testCase struct {
	Slug            string `json:"slug"`
	TesterLogPrefix string `json:"tester_log_prefix"`
	Title           string `json:"title"`
}

// TestCasesJSON builds the test-case list for stages[0..currentIdx]
// (all completed stages rerun as regression, like the real platform).
func TestCasesJSON(stages []course.Stage, currentIdx int) (string, error) {
	var cases []testCase
	for i := 0; i <= currentIdx && i < len(stages); i++ {
		cases = append(cases, testCase{
			Slug:            stages[i].Slug,
			TesterLogPrefix: fmt.Sprintf("stage-%d", i+1),
			Title:           fmt.Sprintf("Stage #%d: %s", i+1, stages[i].Name),
		})
	}
	data, err := json.Marshal(cases)
	return string(data), err
}

// Run executes the official tester against the course's solution dir,
// streaming output to out. Returns nil when all stages pass.
func Run(c course.CourseRef, root string, stages []course.Stage, currentIdx int, out io.Writer) error {
	if err := EnsureTester(c, root, out); err != nil {
		return err
	}
	casesJSON, err := TestCasesJSON(stages, currentIdx)
	if err != nil {
		return err
	}
	cmd := exec.Command(c.TesterBin(root))
	cmd.Dir = c.SolutionDir(root)
	// Grandchildren (the user's program) can inherit our stdout pipe and
	// outlive the tester; without a bound, Wait would block on pipe EOF.
	cmd.WaitDelay = 3 * time.Second
	cmd.Env = append(os.Environ(),
		"CODECRAFTERS_REPOSITORY_DIR="+c.SolutionDir(root),
		"CODECRAFTERS_SUBMISSION_DIR="+c.SolutionDir(root),
		"CODECRAFTERS_TEST_CASES_JSON="+casesJSON,
	)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd.Run()
}
