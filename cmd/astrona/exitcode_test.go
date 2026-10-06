package main

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"astrona/internal/proctor"
)

func TestExitCodes(t *testing.T) {
	if got := exitCodeFor(notPassed("submission did not pass grading")); got != exitNotPassed {
		t.Errorf("not passed = %d", got)
	}
	if got := exitCodeFor(fmt.Errorf("wrapped: %w", notPassed("x"))); got != exitNotPassed {
		t.Errorf("wrapped not passed = %d", got)
	}
	if got := exitCodeFor(errors.New("kubectl isn't installed")); got != exitError {
		t.Errorf("error = %d", got)
	}
}

// --repeat: runs that were graded and failed → 2; any run that broke
// before grading → 1.
func TestRepeatExitCodes(t *testing.T) {
	pass := testRun{pass: true, results: []proctor.CheckResult{{Name: "a", Pass: true}}}
	gradedFail := testRun{err: notPassed("reference solution did not pass grading"), results: []proctor.CheckResult{{Name: "a"}}}
	setupFail := testRun{err: errors.New("lab setup failed")}

	if err := printRepeatSummary(io.Discard, []testRun{pass, gradedFail}); exitCodeFor(err) != exitNotPassed {
		t.Errorf("graded failures only = %v (%d)", err, exitCodeFor(err))
	}
	if err := printRepeatSummary(io.Discard, []testRun{pass, setupFail}); err == nil || exitCodeFor(err) != exitError {
		t.Errorf("setup failure = %v (%d)", err, exitCodeFor(err))
	}
	if err := printRepeatSummary(io.Discard, []testRun{pass, pass}); err != nil {
		t.Errorf("all passed = %v", err)
	}
}
