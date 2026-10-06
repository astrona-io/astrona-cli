package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"astrona/internal/proctor"
)

func run(pass bool, checks ...proctor.CheckResult) testRun {
	var err error
	if !pass {
		err = errors.New("reference solution did not pass grading")
	}
	return testRun{results: checks, pass: pass, err: err}
}

func TestRepeatSummaryAllPass(t *testing.T) {
	runs := []testRun{
		run(true, proctor.CheckResult{Name: "a", Pass: true}),
		run(true, proctor.CheckResult{Name: "a", Pass: true}),
	}
	var buf bytes.Buffer
	if err := printRepeatSummary(&buf, runs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "✓  a  2/2") || !strings.Contains(buf.String(), "All 2 runs passed") {
		t.Errorf("summary:\n%s", buf.String())
	}
	if agg := aggregateRuns(runs); len(agg) != 1 || !agg[0].Pass || agg[0].Message != "passed 2/2 runs" {
		t.Errorf("aggregate = %+v", agg)
	}
}

func TestRepeatSummaryFlakyAndSetupFailure(t *testing.T) {
	runs := []testRun{
		run(true, proctor.CheckResult{Name: "stable", Pass: true}, proctor.CheckResult{Name: "racy", Pass: true}),
		run(false, proctor.CheckResult{Name: "stable", Pass: true}, proctor.CheckResult{Name: "racy", Pass: false}),
		{err: errors.New("lab did not become ready")}, // failed before grading
	}
	var buf bytes.Buffer
	err := printRepeatSummary(&buf, runs)
	if err == nil || !strings.Contains(err.Error(), "2 of 3 runs failed (1 flaky, 0 always-failing check(s), 1 setup failure(s))") {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"⚠  racy    1/2  flaky", "✓  stable  2/2", "run 3 failed before grading: lab did not become ready", "missing waitFor"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q:\n%s", want, buf.String())
		}
	}

	agg := aggregateRuns(runs)
	byName := map[string]proctor.CheckResult{}
	for _, r := range agg {
		byName[r.Name] = r
	}
	if byName["stable"].Pass {
		t.Error("a check that wasn't graded in every run must not count as passing")
	}
	if byName["racy"].Message != "passed 1/3 runs" { // JUnit counts every run
		t.Errorf("racy = %+v", byName["racy"])
	}
	if _, ok := byName["run 3 setup"]; !ok {
		t.Errorf("setup failure missing from JUnit results: %+v", agg)
	}
}
