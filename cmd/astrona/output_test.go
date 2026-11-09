package main

import (
	"strings"
	"testing"
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
	if n := r.section("A", []checkResult{{Name: "x"}, {Name: "y", Status: checkFail}, {Name: "z", Status: checkWarn}}); n != 1 {
		t.Errorf("failed = %d", n)
	}
	if got := checkResultsJSON("A", []checkResult{{Name: "y", Status: checkFail, Hint: "do this"}}); got[0].Status != "fail" || got[0].Fix != "do this" || got[0].Section != "A" {
		t.Errorf("json result = %+v", got[0])
	}
}
