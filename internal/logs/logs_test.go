package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withLogHome points ~/.astrona/logs at a fresh temp dir and returns that
// logs dir. Every test that touches disk uses this for isolation.
func withLogHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir(): %v", err)
	}
	return dir
}

func writeLog(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// sampleLog builds a log body shaped like the real ui output.
func sampleLog(cmd, lab, started string, steps []string, footer string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# astrona %s  lab=%s  %s\n# astrona %s -c ./lab\n\n", cmd, lab, started, cmd)
	b.WriteString("=== Lab: " + lab + " ===\n")
	for _, s := range steps {
		b.WriteString(s + "\n")
	}
	if footer != "" {
		b.WriteString("\n" + footer + "\n")
	}
	return b.String()
}

func TestParseEntryHeaderAndFooter(t *testing.T) {
	dir := withLogHome(t)
	writeLog(t, dir, "run-demo-20260830T120000Z.log", sampleLog(
		"run", "demo", "2026-08-30T12:00:00Z",
		[]string{"--- ok: create cluster (3s) ---", "--- skip: apply manifests (none) ---"},
		"# done: result=ok steps=1/1/0 duration=3s",
	))

	got, err := List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	e := got[0]
	if e.Command != "run" || e.Lab != "demo" {
		t.Errorf("command/lab = %q/%q, want run/demo", e.Command, e.Lab)
	}
	if !e.Started.Equal(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("started = %v, want 2026-08-30T12:00:00Z", e.Started)
	}
	if e.Result != ResultOK {
		t.Errorf("result = %q, want %q", e.Result, ResultOK)
	}
	if e.StepsOK != 1 || e.StepsSkp != 1 || e.StepsErr != 0 {
		t.Errorf("steps = %d/%d/%d, want 1/1/0", e.StepsOK, e.StepsSkp, e.StepsErr)
	}
	if e.Duration != "3s" {
		t.Errorf("duration = %q, want 3s", e.Duration)
	}
	if e.Index != 1 {
		t.Errorf("index = %d, want 1", e.Index)
	}
}

func TestParseEntryResultInference(t *testing.T) {
	dir := withLogHome(t)

	// No footer, a FAIL line present -> fail.
	writeLog(t, dir, "test-a-20260830T110000Z.log", sampleLog(
		"test", "a", "2026-08-30T11:00:00Z",
		[]string{"--- FAIL: check nodes (2s): not ready ---"}, "",
	))
	// No footer, no FAIL line -> ok.
	writeLog(t, dir, "test-b-20260830T100000Z.log", sampleLog(
		"test", "b", "2026-08-30T10:00:00Z",
		[]string{"--- ok: check nodes (2s) ---"}, "",
	))
	// Footer explicitly says fail.
	writeLog(t, dir, "run-c-20260830T090000Z.log", sampleLog(
		"run", "c", "2026-08-30T09:00:00Z",
		[]string{"--- ok: create cluster (1s) ---"},
		"# done: result=fail steps=1/0/1 duration=5s",
	))

	got, err := List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byLab := map[string]Entry{}
	for _, e := range got {
		byLab[e.Lab] = e
	}
	if byLab["a"].Result != ResultFail {
		t.Errorf("lab a result = %q, want fail", byLab["a"].Result)
	}
	if byLab["b"].Result != ResultOK {
		t.Errorf("lab b result = %q, want ok", byLab["b"].Result)
	}
	if byLab["c"].Result != ResultFail {
		t.Errorf("lab c result = %q, want fail", byLab["c"].Result)
	}
}

func TestParseEntryFilenameFallback(t *testing.T) {
	dir := withLogHome(t)
	// Malformed header line: parse must fall back to the filename, and the
	// lab segment keeps its internal dashes.
	writeLog(t, dir, "run-astro-my-lab-20260830T120000Z.log", "garbage header\nmore\n\n--- ok: x (1s) ---\n")

	got, err := List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].Command != "run" || got[0].Lab != "astro-my-lab" {
		t.Errorf("command/lab = %q/%q, want run/astro-my-lab", got[0].Command, got[0].Lab)
	}
	if !got[0].Started.Equal(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("started = %v, want stamp-derived time", got[0].Started)
	}
}

func TestListFiltersAndOrder(t *testing.T) {
	dir := withLogHome(t)
	mk := func(cmd, lab, stamp, rfc string) {
		writeLog(t, dir, fmt.Sprintf("%s-%s-%s.log", cmd, lab, stamp),
			sampleLog(cmd, lab, rfc, []string{"--- ok: x (1s) ---"}, ""))
	}
	mk("run", "alpha", "20260830T090000Z", "2026-08-30T09:00:00Z")
	mk("test", "alpha", "20260830T120000Z", "2026-08-30T12:00:00Z")
	mk("run", "beta", "20260830T100000Z", "2026-08-30T10:00:00Z")

	all, err := List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want 3, got %d", len(all))
	}
	if !all[0].Started.After(all[1].Started) || !all[1].Started.After(all[2].Started) {
		t.Errorf("not newest-first: %v", []time.Time{all[0].Started, all[1].Started, all[2].Started})
	}
	if all[0].Index != 1 || all[2].Index != 3 {
		t.Errorf("indexes not 1..N: %d..%d", all[0].Index, all[2].Index)
	}

	byLab, err := List(Filter{Lab: "alpha"})
	if err != nil {
		t.Fatalf("List lab: %v", err)
	}
	if len(byLab) != 2 {
		t.Errorf("lab=alpha -> %d, want 2", len(byLab))
	}

	byCmd, err := List(Filter{Command: "run"})
	if err != nil {
		t.Fatalf("List command: %v", err)
	}
	if len(byCmd) != 2 {
		t.Errorf("command=run -> %d, want 2", len(byCmd))
	}

	last1, err := List(Filter{Last: 1})
	if err != nil {
		t.Fatalf("List last: %v", err)
	}
	if len(last1) != 1 || last1[0].Lab != "alpha" || last1[0].Command != "test" {
		t.Errorf("last=1 -> %+v, want newest (test/alpha)", last1)
	}
}

func TestListSinceWindow(t *testing.T) {
	dir := withLogHome(t)
	old := time.Now().Add(-72 * time.Hour).UTC()
	recent := time.Now().Add(-1 * time.Hour).UTC()
	writeLog(t, dir, "run-old-"+old.Format(stampLayout)+".log",
		sampleLog("run", "old", old.Format(time.RFC3339), []string{"--- ok: x (1s) ---"}, ""))
	writeLog(t, dir, "run-new-"+recent.Format(stampLayout)+".log",
		sampleLog("run", "new", recent.Format(time.RFC3339), []string{"--- ok: x (1s) ---"}, ""))

	got, err := List(Filter{Since: 24 * time.Hour})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Lab != "new" {
		t.Errorf("since=24h -> %+v, want only lab new", got)
	}
}

func TestResolve(t *testing.T) {
	dir := withLogHome(t)
	p1 := writeLog(t, dir, "run-alpha-20260830T090000Z.log",
		sampleLog("run", "alpha", "2026-08-30T09:00:00Z", []string{"--- ok: x (1s) ---"}, ""))
	writeLog(t, dir, "test-beta-20260830T120000Z.log",
		sampleLog("test", "beta", "2026-08-30T12:00:00Z", []string{"--- ok: x (1s) ---"}, ""))

	cases := []struct {
		ref     string
		f       Filter
		wantLab string
	}{
		{"", Filter{}, "beta"},
		{"latest", Filter{}, "beta"},
		{"", Filter{Command: "run"}, "alpha"},
		{"1", Filter{}, "beta"},
		{"2", Filter{}, "alpha"},
		{"20260830T090000Z", Filter{}, "alpha"},
		{"run-alpha-20260830T090000Z.log", Filter{}, "alpha"},
		{p1, Filter{}, "alpha"},
	}
	for _, c := range cases {
		got, err := Resolve(c.ref, c.f)
		if err != nil {
			t.Errorf("Resolve(%q, %+v): %v", c.ref, c.f, err)
			continue
		}
		if got.Lab != c.wantLab {
			t.Errorf("Resolve(%q, %+v) -> %q, want %q", c.ref, c.f, got.Lab, c.wantLab)
		}
	}

	if _, err := Resolve("99", Filter{}); err == nil {
		t.Error("Resolve out-of-range index: want error, got nil")
	}
	if _, err := Resolve("nope", Filter{}); err == nil {
		t.Error("Resolve unknown ref: want error, got nil")
	}
}

func TestResolveNoLogs(t *testing.T) {
	withLogHome(t)
	if _, err := Resolve("", Filter{}); err == nil {
		t.Error("Resolve with no logs: want error, got nil")
	}
}

func TestParseClassifies(t *testing.T) {
	dir := withLogHome(t)
	writeLog(t, dir, "run-demo-20260830T120000Z.log", sampleLog(
		"run", "demo", "2026-08-30T12:00:00Z",
		[]string{
			"--- create cluster ---",
			"some raw subprocess output",
			"--- ok: create cluster (3s) ---",
			"[WARN] something odd",
			"--- skip: manifests (none) ---",
			"--- FAIL: grade (1s): nope ---",
		},
		"# done: result=fail steps=1/1/1 duration=4s",
	))

	e, err := Resolve("", Filter{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	l, err := Parse(e)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	kinds := map[string]int{}
	for _, ln := range l.Lines {
		kinds[ln.Kind]++
	}
	for _, want := range []string{KindHeader, KindSection, KindStep, KindOK, KindSkip, KindFail, KindWarn, KindDone, KindOutput} {
		if kinds[want] == 0 {
			t.Errorf("no line classified as %q; got %+v", want, kinds)
		}
	}
}

func TestPrune(t *testing.T) {
	dir := withLogHome(t)
	stamps := []string{
		"20260830T090000Z", "20260830T100000Z", "20260830T110000Z", "20260830T120000Z",
	}
	for i, s := range stamps {
		rfc := "2026-08-30T" + s[9:11] + ":00:00Z"
		writeLog(t, dir, fmt.Sprintf("run-l%d-%s.log", i, s),
			sampleLog("run", fmt.Sprintf("l%d", i), rfc, []string{"--- ok: x (1s) ---"}, ""))
	}

	// Dry run keeps the 2 newest, reports the other 2, deletes nothing.
	doomed, err := Prune(Filter{}, 2, 0, true)
	if err != nil {
		t.Fatalf("Prune dry: %v", err)
	}
	if len(doomed) != 2 {
		t.Fatalf("dry run -> %d doomed, want 2", len(doomed))
	}
	if n := countLogs(t, dir); n != 4 {
		t.Fatalf("dry run deleted files: %d left, want 4", n)
	}

	// Real run with the same params.
	removed, err := Prune(Filter{}, 2, 0, false)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d, want 2", len(removed))
	}
	if n := countLogs(t, dir); n != 2 {
		t.Fatalf("%d logs left, want 2", n)
	}

	left, _ := List(Filter{})
	if left[0].Lab != "l3" || left[1].Lab != "l2" {
		t.Errorf("kept %q,%q, want l3,l2 (the 2 newest)", left[0].Lab, left[1].Lab)
	}
}

func TestPruneOlderThan(t *testing.T) {
	dir := withLogHome(t)
	old := time.Now().Add(-72 * time.Hour).UTC()
	recent := time.Now().Add(-1 * time.Hour).UTC()
	writeLog(t, dir, "run-old-"+old.Format(stampLayout)+".log",
		sampleLog("run", "old", old.Format(time.RFC3339), []string{"--- ok: x (1s) ---"}, ""))
	writeLog(t, dir, "run-new-"+recent.Format(stampLayout)+".log",
		sampleLog("run", "new", recent.Format(time.RFC3339), []string{"--- ok: x (1s) ---"}, ""))

	removed, err := Prune(Filter{}, 0, 24*time.Hour, false)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(removed) != 1 || removed[0].Lab != "old" {
		t.Fatalf("removed %+v, want only lab old", removed)
	}
	if n := countLogs(t, dir); n != 1 {
		t.Errorf("%d logs left, want 1", n)
	}
}

func countLogs(t *testing.T, dir string) int {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return len(m)
}
