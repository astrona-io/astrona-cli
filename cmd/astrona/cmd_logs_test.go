package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/logs"

	"github.com/spf13/cobra"
)

// seedLog points $HOME at a temp dir and drops one run log into
// ~/.astrona/logs, returning its path.
func seedLog(t *testing.T, name, content string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	dir, err := logs.Dir()
	if err != nil {
		t.Fatalf("logs.Dir(): %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture log: %v", err)
	}
	return p
}

const fixtureLog = "# astrona run  lab=demo  2026-08-30T12:00:00Z\n" +
	"# astrona run -c ./lab\n\n" +
	"=== Lab: demo ===\n" +
	"--- create cluster ---\n" +
	"kind output here\n" +
	"--- ok: create cluster (3s) ---\n" +
	"\n# done: result=ok steps=1/0/0 duration=3s\n"

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var b bytes.Buffer
		_, _ = io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

func TestNewLogsCmd(t *testing.T) {
	cmd := newLogsCmd()

	if cmd.Use != "logs" {
		t.Errorf("Use = %q, want logs", cmd.Use)
	}
	hasAlias := false
	for _, a := range cmd.Aliases {
		if a == "log" {
			hasAlias = true
		}
	}
	if !hasAlias {
		t.Errorf("aliases = %v, want to include log", cmd.Aliases)
	}

	want := map[string]bool{"list": false, "view": false, "path": false, "export": false, "clean": false}
	for _, c := range cmd.Commands() {
		name := strings.Fields(c.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("subcommand %q not registered", name)
		}
	}
}

func TestLogsCmdFlags(t *testing.T) {
	checks := []struct {
		cmd   func() *cobra.Command
		flags []string
	}{
		{newLogsListCmd, []string{"format", "lab", "command", "since", "last"}},
		{newLogsViewCmd, []string{"format", "no-pager", "tail", "grep", "lab", "command"}},
		{newLogsPathCmd, []string{"all", "lab", "command"}},
		{newLogsExportCmd, []string{"output", "format", "all", "force", "lab", "command"}},
		{newLogsCleanCmd, []string{"older-than", "keep", "dry-run", "yes", "lab", "command"}},
	}
	for _, c := range checks {
		cmd := c.cmd()
		for _, f := range c.flags {
			if cmd.Flag(f) == nil {
				t.Errorf("%s: flag %q not defined", cmd.Use, f)
			}
		}
	}
}

func TestLogsListJSON(t *testing.T) {
	seedLog(t, "run-demo-20260830T120000Z.log", fixtureLog)

	out := captureStdout(t, func() {
		cmd := newLogsListCmd()
		cmd.SetArgs([]string{"--format", "json"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})

	var entries []logs.Entry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if entries[0].Command != "run" || entries[0].Lab != "demo" || entries[0].Result != "ok" {
		t.Errorf("entry = %+v, want run/demo/ok", entries[0])
	}
}

func TestLogsListTable(t *testing.T) {
	seedLog(t, "run-demo-20260830T120000Z.log", fixtureLog)

	out := captureStdout(t, func() {
		cmd := newLogsListCmd()
		cmd.SetArgs(nil)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	for _, want := range []string{"COMMAND", "run", "demo", "OK"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}
}

func TestLogsViewRaw(t *testing.T) {
	seedLog(t, "run-demo-20260830T120000Z.log", fixtureLog)

	out := captureStdout(t, func() {
		cmd := newLogsViewCmd()
		cmd.SetArgs([]string{"--no-pager"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if !strings.Contains(out, "# astrona run  lab=demo") {
		t.Errorf("view output missing header:\n%s", out)
	}
	if !strings.Contains(out, "kind output here") {
		t.Errorf("view output missing body:\n%s", out)
	}
}

func TestLogsViewGrep(t *testing.T) {
	seedLog(t, "run-demo-20260830T120000Z.log", fixtureLog)

	out := captureStdout(t, func() {
		cmd := newLogsViewCmd()
		cmd.SetArgs([]string{"--grep", "^--- ok:"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if strings.Contains(out, "kind output here") {
		t.Errorf("grep should have filtered out non-matching lines:\n%s", out)
	}
	if !strings.Contains(out, "--- ok: create cluster") {
		t.Errorf("grep dropped the matching line:\n%s", out)
	}
}

func TestLogsPath(t *testing.T) {
	p := seedLog(t, "run-demo-20260830T120000Z.log", fixtureLog)

	out := captureStdout(t, func() {
		cmd := newLogsPathCmd()
		cmd.SetArgs(nil)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
	if strings.TrimSpace(out) != p {
		t.Errorf("path = %q, want %q", strings.TrimSpace(out), p)
	}
}

func TestLogsExport(t *testing.T) {
	seedLog(t, "run-demo-20260830T120000Z.log", fixtureLog)
	dest := filepath.Join(t.TempDir(), "out.log")

	run := func(args ...string) error {
		cmd := newLogsExportCmd()
		cmd.SetArgs(args)
		return cmd.Execute()
	}

	if err := captureErr(t, func() error { return run("-o", dest) }); err != nil {
		t.Fatalf("first export: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(data), "# astrona run  lab=demo") {
		t.Errorf("exported file missing header:\n%s", data)
	}

	// Second export to the same path must refuse without --force.
	if err := captureErr(t, func() error { return run("-o", dest) }); err == nil {
		t.Error("re-export without --force: want error, got nil")
	}
	// With --force it overwrites.
	if err := captureErr(t, func() error { return run("-o", dest, "--force") }); err != nil {
		t.Errorf("re-export with --force: %v", err)
	}
}

func TestLogsExportJSON(t *testing.T) {
	seedLog(t, "run-demo-20260830T120000Z.log", fixtureLog)
	dest := filepath.Join(t.TempDir(), "out.json")

	err := captureErr(t, func() error {
		cmd := newLogsExportCmd()
		cmd.SetArgs([]string{"-o", dest, "--format", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("export json: %v", err)
	}

	var parsed logs.Log
	data, _ := os.ReadFile(dest)
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal exported json: %v", err)
	}
	if parsed.Meta.Lab != "demo" || len(parsed.Lines) == 0 {
		t.Errorf("exported json = %+v, want meta.lab demo and non-empty lines", parsed)
	}
}

func TestLogsClean(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := logs.Dir()
	if err != nil {
		t.Fatalf("logs.Dir(): %v", err)
	}
	for _, hh := range []string{"09", "10", "11"} {
		body := "# astrona run  lab=demo  2026-08-30T" + hh + ":00:00Z\n" +
			"# astrona run -c ./lab\n\n--- ok: create cluster (1s) ---\n" +
			"\n# done: result=ok steps=1/0/0 duration=1s\n"
		if err := os.WriteFile(filepath.Join(dir, "run-demo-20260830T"+hh+"0000Z.log"), []byte(body), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// Without --yes: preview only, nothing deleted.
	_ = captureStdout(t, func() {
		cmd := newLogsCleanCmd()
		cmd.SetArgs([]string{"--keep", "1"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("clean preview: %v", err)
		}
	})
	if n := len(mustGlob(t, dir)); n != 3 {
		t.Fatalf("preview deleted files: %d left, want 3", n)
	}

	// With --yes: keep 1 newest, delete the other 2.
	_ = captureStdout(t, func() {
		cmd := newLogsCleanCmd()
		cmd.SetArgs([]string{"--keep", "1", "--yes"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("clean: %v", err)
		}
	})
	left := mustGlob(t, dir)
	if len(left) != 1 {
		t.Fatalf("%d logs left, want 1", len(left))
	}
	if !strings.Contains(left[0], "20260830T110000Z") {
		t.Errorf("kept %q, want the newest (…T110000Z)", left[0])
	}
}

func mustGlob(t *testing.T, dir string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, "*.log"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	return m
}

// captureErr runs fn while discarding stdout, returning fn's error. Used by
// tests that only care about the error, not the printed "wrote …" lines.
func captureErr(t *testing.T, fn func() error) error {
	t.Helper()
	var got error
	_ = captureStdout(t, func() { got = fn() })
	return got
}
