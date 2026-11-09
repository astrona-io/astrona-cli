package labops

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/proctor"
)

func TestPickRunningLab(t *testing.T) {
	states := map[string]*labstate.State{
		"astro-a": {Source: &labstate.Source{Catalog: "ATS014/section-010/module-01/lab-02"}},
		"astro-b": {Source: &labstate.Source{Config: "/home/me/labs/b"}},
		"astro-g": {Source: &labstate.Source{Git: "https://github.com/org/labs", Config: "net-01", GitRef: "v2"}},
	}
	load := func(name string) (*labstate.State, error) { return states[name], nil }

	// Nothing running: no lab, no error — the usual "no lab here" follows.
	if name, src, err := PickRunningLab(nil, load); name != "" || src != nil || err != nil {
		t.Errorf("none running: %q, %v, %v", name, src, err)
	}
	// Exactly one: that one, through what run remembered.
	name, src, err := PickRunningLab([]string{"astro-b"}, load)
	if err != nil || name != "astro-b" || src.Config != "/home/me/labs/b" {
		t.Errorf("one running: %q, %+v, %v", name, src, err)
	}
	// One, but started by an older astrona: say how to name it.
	if _, _, err := PickRunningLab([]string{"astro-old"}, load); err == nil || !strings.Contains(err.Error(), "astrona submit -c <lab-dir>") {
		t.Errorf("unknown config: %v", err)
	}
	// Several: list them with how to name each.
	_, _, err = PickRunningLab([]string{"astro-a", "astro-b", "astro-g", "astro-old"}, load)
	if err == nil {
		t.Fatal("several running: no error")
	}
	for _, want := range []string{
		"several labs are running",
		"astro-a    astrona submit ATS014/section-010/module-01/lab-02",
		"astro-b    astrona submit -c /home/me/labs/b",
		"astro-g    astrona submit --git https://github.com/org/labs -c net-01 --git-ref v2",
		"astro-old    (started by an older astrona",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("several running: %q missing from\n%s", want, err)
		}
	}
}

func TestLabOfVM(t *testing.T) {
	known := []string{"astro-net", "astro-net-lab"}
	for vm, want := range map[string]string{
		"astro-net":            "astro-net",
		"astro-net-router":     "astro-net",
		"astro-net-lab-client": "astro-net-lab",
		"astro-other":          "astro-other",
	} {
		if got := LabOfVM(vm, known); got != want {
			t.Errorf("LabOfVM(%q) = %q, want %q", vm, got, want)
		}
	}
}

func TestCapCheckTexts(t *testing.T) {
	long := strings.Repeat("é", MaxSentCheckText) // 2 bytes each
	in := Submission{Checks: []SubmissionCheck{{Name: "a", Message: "ok\x1b[31m red\x00\nline\ttab", Hint: long}}}
	out := CapCheckTexts(in)
	if got := out.Checks[0].Message; got != "ok[31m red\nline\ttab" {
		t.Errorf("message = %q", got)
	}
	hint := out.Checks[0].Hint
	if len(hint) > MaxSentCheckText+len("…") || !strings.HasSuffix(hint, "…") || !utf8.ValidString(hint) {
		t.Errorf("hint: %d bytes, valid %v", len(hint), utf8.ValidString(hint))
	}
	if in.Checks[0].Hint != long {
		t.Error("the result printed and recorded here was changed")
	}
}

func TestSubmissionJSONHidesHints(t *testing.T) {
	results := []proctor.CheckResult{{Name: "a", Pass: true, Points: 1}, {Name: "b", Points: 2, Hint: "try kubectl scale", Duration: 30 * time.Millisecond}}
	cfg := &config.LabConfig{}
	got := NewSubmission(cfg, "astro-x", results, false, false, 3, false, time.Unix(0, 0))
	if got.Earned != 1 || got.Max != 3 || got.Attempt != 3 || got.Checks[1].Hint != "try kubectl scale" || got.Checks[1].DurationMs != 30 {
		t.Errorf("submission = %+v", got)
	}
	if hidden := NewSubmission(cfg, "astro-x", results, false, false, 1, true, time.Unix(0, 0)); hidden.Checks[1].Hint != "" {
		t.Error("hint leaked under exam conditions")
	}
}
