package main

import (
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestDoctorVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	old := Version
	defer func() { Version = old }()
	Version = "v0.2.2"

	for _, c := range []struct {
		constraint string
		status     checkStatus
		detail     string
	}{
		{"", checkOK, "any astrona"},
		{">=0.2.0", checkOK, "fits"},
		{"<=0.2.1", checkFail, "no fitting version is installed"},
		{"~0.2", checkFail, "astronaVersion"},
	} {
		r := doctorVersion(&config.LabConfig{AstronaVersion: c.constraint})
		if r.status != c.status || !strings.Contains(r.detail, c.detail) {
			t.Errorf("%q = %v %q", c.constraint, r.status, r.detail)
		}
	}
	if err := doctorVerdict(0); err != nil {
		t.Errorf("no problems = %v", err)
	}
	if err := doctorVerdict(2); err == nil || !strings.Contains(err.Error(), "2 problem(s)") {
		t.Errorf("problems = %v", err)
	}
}
