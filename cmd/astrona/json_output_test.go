package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runRoot runs astrona with args and returns what it printed on stdout.
func runRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("ASTRONA_NO_UPDATE_CHECK", "1")
	root := newRootCmd(&rootFlags{})
	root.SetArgs(args)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	var err error
	out := captureStdout(t, func() { err = root.Execute() })
	return out, err
}

// labDir writes config as a lab's config.yaml and returns its directory.
func labDir(t *testing.T, config string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A lab config that can't be loaded is still one JSON report on stdout,
// with a fail row, and a non-zero exit.
func TestJSONReportOnLoadError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	broken := labDir(t, "metadata: [oops\n")
	missing := filepath.Join(t.TempDir(), "nope")
	cases := [][]string{
		{"validate", "-c", broken, "-o", "json"},
		{"validate", "-c", missing, "-o", "json"},
		{"check", "-c", broken, "-o", "json"},
	}
	for _, args := range cases {
		out, err := runRoot(t, args...)
		if err == nil {
			t.Errorf("%v: want an error", args)
		}
		var doc struct {
			OK       bool `json:"ok"`
			Problems int  `json:"problems"`
			Results  []struct {
				Section, Name, Status string
			} `json:"results"`
		}
		if !json.Valid([]byte(out)) || json.Unmarshal([]byte(out), &doc) != nil {
			t.Errorf("%v: stdout isn't one JSON document: %q", args, out)
			continue
		}
		last := doc.Results[len(doc.Results)-1]
		if doc.OK || doc.Problems == 0 || last.Name != "lab config" || last.Status != "fail" {
			t.Errorf("%v: report = %+v", args, doc)
		}
	}
}

func TestUseJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := labDir(t, "metadata: {name: use-json}\n")

	out, err := runRoot(t, "use", dir, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var c currentLab
	if err := json.Unmarshal([]byte(out), &c); err != nil || c.Name != "use-json" {
		t.Errorf("use <lab> -o json = %q (%v)", out, err)
	}

	out, err = runRoot(t, "use", "--clear", "-o", "json")
	if err != nil || strings.TrimSpace(out) != "null" {
		t.Errorf("use --clear -o json = %q (%v), want null", out, err)
	}

	// A bad -o is refused before anything is remembered.
	if _, err := runRoot(t, "use", dir, "-o", "yaml"); err == nil {
		t.Error("use -o yaml: want an error")
	}
	if c, _ := loadCurrentLab(); c != nil {
		t.Errorf("lab remembered despite a bad -o: %+v", c)
	}
}

func TestLogsListJSONEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	out, err := runRoot(t, "logs", "list", "-o", "json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("logs list -o json with no logs = %q (%v), want []", out, err)
	}
}

// Questions go to stderr so stdout stays the command's output.
func TestPromptsGoToStderr(t *testing.T) {
	if promptOut != os.Stderr {
		t.Error("promptOut isn't os.Stderr")
	}
}
