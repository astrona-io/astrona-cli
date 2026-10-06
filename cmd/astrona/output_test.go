package main

import (
	"strings"
	"testing"
	"time"

	"astrona/internal/config"
	"astrona/internal/proctor"
)

func TestCheckOutput(t *testing.T) {
	for _, ok := range []string{"", "json"} {
		if err := checkOutput(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	if err := checkOutput("wide", "wide"); err != nil {
		t.Errorf("wide where allowed: %v", err)
	}
	if err := checkOutput("yaml"); err == nil || !strings.Contains(err.Error(), "use json") {
		t.Errorf("yaml = %v", err)
	}
}

func TestReportCountsFailures(t *testing.T) {
	r := &report{json: true}
	if n := r.section("A", []checkResult{{name: "x"}, {name: "y", status: checkFail}, {name: "z", status: checkWarn}}); n != 1 {
		t.Errorf("failed = %d", n)
	}
	if got := checkResultsJSON("A", []checkResult{{name: "y", status: checkFail, hint: "do this"}}); got[0].Status != "fail" || got[0].Fix != "do this" || got[0].Section != "A" {
		t.Errorf("json result = %+v", got[0])
	}
}

func TestSubmissionJSONHidesHints(t *testing.T) {
	results := []proctor.CheckResult{{Name: "a", Pass: true, Points: 1}, {Name: "b", Points: 2, Hint: "try kubectl scale", Duration: 30 * time.Millisecond}}
	cfg := &config.LabConfig{}
	got := submissionJSON(cfg, "astro-x", results, false, false, 3, false, time.Unix(0, 0))
	if got.Earned != 1 || got.Max != 3 || got.Attempt != 3 || got.Checks[1].Hint != "try kubectl scale" || got.Checks[1].DurationMs != 30 {
		t.Errorf("submission = %+v", got)
	}
	if hidden := submissionJSON(cfg, "astro-x", results, false, false, 1, true, time.Unix(0, 0)); hidden.Checks[1].Hint != "" {
		t.Error("hint leaked under exam conditions")
	}
}
