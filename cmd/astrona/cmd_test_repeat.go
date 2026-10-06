package main

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"astrona/internal/proctor"
)

// maxTestRepeat bounds `astrona test --repeat` — every run creates and
// destroys a whole environment.
const maxTestRepeat = 20

// testRun is one lifecycle's outcome. results is nil when the run failed
// before grading (setup, bootstrap, readiness).
type testRun struct {
	results []proctor.CheckResult
	pass    bool
	err     error
}

// checkTally counts how often a check passed across graded runs, in
// first-seen order.
type checkTally struct {
	name          string
	passed, total int
	duration      time.Duration
}

func tallyChecks(runs []testRun) []checkTally {
	var order []string
	byName := map[string]*checkTally{}
	for _, r := range runs {
		for _, c := range r.results {
			t, ok := byName[c.Name]
			if !ok {
				t = &checkTally{name: c.Name}
				byName[c.Name] = t
				order = append(order, c.Name)
			}
			t.total++
			t.duration += c.Duration
			if c.Pass {
				t.passed++
			}
		}
	}
	out := make([]checkTally, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out
}

// aggregateRuns folds every run into one result per check for the JUnit
// report: passing only if it passed in every run that was graded, and
// failing outright if any run didn't get as far as grading.
func aggregateRuns(runs []testRun) []proctor.CheckResult {
	var out []proctor.CheckResult
	for _, t := range tallyChecks(runs) {
		out = append(out, proctor.CheckResult{
			Name:     t.name,
			Pass:     t.passed == t.total && t.total == len(runs),
			Message:  fmt.Sprintf("passed %d/%d runs", t.passed, len(runs)),
			Duration: t.duration / time.Duration(max(t.total, 1)),
		})
	}
	for i, r := range runs {
		if r.results == nil && r.err != nil {
			out = append(out, proctor.CheckResult{Name: fmt.Sprintf("run %d setup", i+1), Message: r.err.Error()})
		}
	}
	return out
}

// printRepeatSummary reports consistency across runs and returns an error
// if any run failed — a flaky lab must fail CI.
func printRepeatSummary(w io.Writer, runs []testRun) error {
	fmt.Fprintf(w, "\nRepeat summary — %d runs:\n\n", len(runs))
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	flaky, failing := 0, 0
	for _, t := range tallyChecks(runs) {
		// Judged against the runs where the check was graded — a run that
		// failed during setup is reported on its own below.
		mark, note := "✓", ""
		switch t.passed {
		case t.total:
		case 0:
			mark, note = "✗", "never passed"
			failing++
		default:
			mark, note = "⚠", "flaky"
			flaky++
		}
		fmt.Fprintf(tw, "  %s\t%s\t%d/%d\t%s\n", mark, t.name, t.passed, t.total, note)
	}
	tw.Flush()

	setupFailures := 0
	for i, r := range runs {
		if r.results == nil && r.err != nil {
			setupFailures++
			fmt.Fprintf(w, "  ✗ run %d failed before grading: %s\n", i+1, r.err)
		}
	}

	failedRuns := 0
	for _, r := range runs {
		if r.err != nil {
			failedRuns++
		}
	}
	if failedRuns == 0 {
		fmt.Fprintf(w, "\nAll %d runs passed — no flaky checks.\n", len(runs))
		return nil
	}
	fmt.Fprintf(w, "\n%d of %d runs failed", failedRuns, len(runs))
	if flaky > 0 {
		fmt.Fprintf(w, " · %d flaky check(s) — usually a missing waitFor before grading", flaky)
	}
	fmt.Fprintln(w)
	return fmt.Errorf("%d of %d runs failed (%d flaky, %d always-failing check(s), %d setup failure(s))", failedRuns, len(runs), flaky, failing, setupFailures)
}
