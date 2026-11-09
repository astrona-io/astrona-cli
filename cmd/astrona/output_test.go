package main

import (
	"strings"
	"testing"

	"astrona/internal/preflight"
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
	if n := r.section("A", []preflight.Result{{Name: "x"}, {Name: "y", Status: preflight.Fail}, {Name: "z", Status: preflight.Warn}}); n != 1 {
		t.Errorf("failed = %d", n)
	}
	if got := checkResultsJSON("A", []preflight.Result{{Name: "y", Status: preflight.Fail, Hint: "do this"}}); got[0].Status != "fail" || got[0].Fix != "do this" || got[0].Section != "A" {
		t.Errorf("json result = %+v", got[0])
	}
}
