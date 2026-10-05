package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"astrona/internal/proctor"
)

func TestPrintProgressAndAttempts(t *testing.T) {
	a1 := proctor.Attempt{Time: time.Now(), Earned: 1, Max: 3, Checks: []proctor.AttemptCheck{{Name: "svc", Pass: false}, {Name: "pod", Pass: true}}}
	a2 := proctor.Attempt{Time: time.Now(), Earned: 3, Max: 3, Pass: true, Checks: []proctor.AttemptCheck{{Name: "svc", Pass: true}, {Name: "pod", Pass: true}}}

	var buf bytes.Buffer
	printProgress(&buf, nil, a1)
	if !strings.Contains(buf.String(), "Attempt #1 recorded") {
		t.Errorf("first = %q", buf.String())
	}
	buf.Reset()
	printProgress(&buf, []proctor.Attempt{a1}, a2)
	for _, want := range []string{"Attempt #2: 3/3 (+2 point(s) since #1)", "now passing: svc"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("progress missing %q:\n%s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), "pod") {
		t.Error("unchanged check reported")
	}

	buf.Reset()
	printAttempts(&buf, "astro-x", []proctor.Attempt{a1, a2})
	for _, want := range []string{"#", "1/3", "FAIL", "3/3", "PASS", "Best: 3/3 · 2 attempt(s)"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("history missing %q:\n%s", want, buf.String())
		}
	}
	buf.Reset()
	printAttempts(&buf, "astro-x", nil)
	if !strings.Contains(buf.String(), "No attempts recorded") {
		t.Errorf("empty = %q", buf.String())
	}
}

func TestPrintAttemptsTimed(t *testing.T) {
	var buf bytes.Buffer
	printAttempts(&buf, "astro-x", []proctor.Attempt{
		{Time: time.Now(), Earned: 1, Max: 4, Timed: true, ElapsedSeconds: 0},
		{Time: time.Now(), Earned: 2, Max: 4, Timed: true, ElapsedSeconds: 1800},
		{Time: time.Now(), Earned: 4, Max: 4, Pass: false, Timed: true, ElapsedSeconds: 66, OverTime: true},
	})
	for _, want := range []string{"TIME USED", "0s", "30m0s", "1m6s (over)"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q:\n%s", want, buf.String())
		}
	}
}
