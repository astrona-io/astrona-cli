package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func titles(steps []setupStep) []string {
	var out []string
	for _, s := range steps {
		out = append(out, s.title)
	}
	return out
}

func TestPlanSetupNothingToDo(t *testing.T) {
	env := setupEnv{goos: "darwin", hasBrew: true, missing: map[string]bool{}, hasPodman: true, engineReachable: true}
	if steps := planSetup(env); len(steps) != 0 {
		t.Fatalf("want no steps, got %v", titles(steps))
	}
}

func TestPlanSetupFreshMacWithBrew(t *testing.T) {
	env := setupEnv{goos: "darwin", hasBrew: true, missing: map[string]bool{"kind": true, "kubectl": true, "docker or podman": true}}
	steps := planSetup(env)
	if len(steps) != 2 {
		t.Fatalf("want brew install + podman machine, got %v", titles(steps))
	}
	if got := strings.Join(steps[0].commands[0], " "); got != "brew install kind kubernetes-cli podman" {
		t.Errorf("brew step = %q", got)
	}
	if len(steps[1].commands) != 2 || steps[1].commands[0][2] != "init" || steps[1].commands[1][2] != "start" || !steps[1].waitForEngine {
		t.Errorf("podman machine step = %+v", steps[1])
	}
}

func TestPlanSetupMacWithoutBrewSendsToHomebrew(t *testing.T) {
	env := setupEnv{goos: "darwin", missing: map[string]bool{"kind": true}}
	steps := planSetup(env)
	if len(steps) != 1 || steps[0].manual == "" || !strings.Contains(steps[0].manual, "brew.sh") {
		t.Fatalf("want one manual Homebrew step, got %+v", steps)
	}
}

func TestPlanSetupStoppedPodmanMachineOnlyStarts(t *testing.T) {
	env := setupEnv{goos: "darwin", hasBrew: true, missing: map[string]bool{}, hasPodman: true, podmanMachineExists: true}
	steps := planSetup(env)
	if len(steps) != 1 || len(steps[0].commands) != 1 || strings.Join(steps[0].commands[0], " ") != "podman machine start" {
		t.Fatalf("want only podman machine start, got %+v", steps)
	}
}

func TestPlanSetupStoppedDockerDesktop(t *testing.T) {
	env := setupEnv{goos: "darwin", hasBrew: true, missing: map[string]bool{}, hasDocker: true}
	steps := planSetup(env)
	if len(steps) != 1 || strings.Join(steps[0].commands[0], " ") != "open -a Docker" {
		t.Fatalf("want open -a Docker, got %+v", steps)
	}
}

func TestPlanSetupLinuxWithoutBrewDownloadsAndAsksForEngine(t *testing.T) {
	env := setupEnv{goos: "linux", goarch: "amd64", missing: map[string]bool{"kind": true, "kubectl": true, "docker or podman": true}, localBin: "/home/s/.local/bin"}
	steps := planSetup(env)
	var downloads, manuals int
	for _, s := range steps {
		if s.download != nil {
			downloads++
			if s.download.dir != "/home/s/.local/bin" {
				t.Errorf("download dir = %q", s.download.dir)
			}
		}
		if s.manual != "" {
			manuals++
		}
		if len(s.commands) > 0 {
			t.Errorf("Linux without brew must not run commands (no sudo), got %v", s.commands)
		}
	}
	if downloads != 2 {
		t.Errorf("want kind + kubectl downloads, got %d (%v)", downloads, titles(steps))
	}
	if manuals != 2 { // container engine + PATH
		t.Errorf("want engine and PATH manual steps, got %d (%v)", manuals, titles(steps))
	}
}

func TestPlanSetupLinuxBrewNeverInstallsPodman(t *testing.T) {
	env := setupEnv{goos: "linux", hasBrew: true, missing: map[string]bool{"kind": true, "docker or podman": true}, localBinOnPath: true}
	steps := planSetup(env)
	if got := strings.Join(steps[0].commands[0], " "); got != "brew install kind" {
		t.Errorf("brew step = %q", got)
	}
}

func TestSetupRunnerDryRunRunsNothing(t *testing.T) {
	var out bytes.Buffer
	ran := false
	r := setupRunner{in: strings.NewReader(""), out: &out, dryRun: true, run: func([]string) error { ran = true; return nil }}
	if _, err := r.execute([]setupStep{{title: "x", commands: [][]string{{"brew", "install", "kind"}}}}); err != nil {
		t.Fatal(err)
	}
	if ran || !strings.Contains(out.String(), "$ brew install kind") {
		t.Fatalf("dry run ran=%v out=%q", ran, out.String())
	}
}

func TestSetupRunnerAsksAndSkipsOnNo(t *testing.T) {
	old := promptOut
	promptOut = &bytes.Buffer{}
	defer func() { promptOut = old }()
	var out bytes.Buffer
	ran := false
	r := setupRunner{in: strings.NewReader("n\n"), out: &out, run: func([]string) error { ran = true; return nil }}
	manual, err := r.execute([]setupStep{{title: "x", commands: [][]string{{"true"}}}})
	if err != nil || ran || manual != 1 {
		t.Fatalf("ran=%v manual=%d err=%v", ran, manual, err)
	}
}

func TestSetupRunnerStopsOnFailure(t *testing.T) {
	r := setupRunner{in: strings.NewReader(""), out: &bytes.Buffer{}, yes: true, run: func([]string) error { return errors.New("boom") }}
	if _, err := r.execute([]setupStep{{title: "x", commands: [][]string{{"brew", "install", "kind"}}}}); err == nil || !strings.Contains(err.Error(), "brew install kind") {
		t.Fatalf("want error naming the command, got %v", err)
	}
}

func TestSetupRunnerWaitsForEngine(t *testing.T) {
	waited := false
	r := setupRunner{in: strings.NewReader(""), out: &bytes.Buffer{}, yes: true,
		run:    func([]string) error { return nil },
		waitUp: func(time.Duration) bool { waited = true; return true }}
	if _, err := r.execute([]setupStep{{title: "x", commands: [][]string{{"podman", "machine", "start"}}, waitForEngine: true}}); err != nil || !waited {
		t.Fatalf("waited=%v err=%v", waited, err)
	}
}

func TestParseChecksum(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	for _, in := range []string{sum, sum + "  kind-linux-amd64\n", strings.ToUpper(sum)} {
		got, err := parseChecksum(in)
		if err != nil || got != sum {
			t.Errorf("parseChecksum(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "nothex", strings.Repeat("ab", 31)} {
		if _, err := parseChecksum(bad); err == nil {
			t.Errorf("parseChecksum(%q) accepted", bad)
		}
	}
}

// installToolDownload verifies the checksum before anything lands in dir.
func TestInstallToolDownloadVerifiesChecksum(t *testing.T) {
	body := []byte("#!/bin/sh\necho kind\n")
	sum := sha256.Sum256(body)
	good := hex.EncodeToString(sum[:])
	for _, tc := range []struct {
		name    string
		sum     string
		wantErr bool
	}{{"match", good, false}, {"mismatch", strings.Repeat("00", 32), true}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".sha256sum") {
					w.Write([]byte(tc.sum + "  kind\n"))
					return
				}
				w.Write(body)
			}))
			defer srv.Close()
			old := toolURLs
			toolURLs = func(context.Context, toolDownload) (string, string, error) {
				return srv.URL + "/kind", srv.URL + "/kind.sha256sum", nil
			}
			defer func() { toolURLs = old }()
			dir := t.TempDir()
			path, err := installToolDownload(context.Background(), &toolDownload{tool: "kind", dir: dir})
			if tc.wantErr {
				if err == nil {
					t.Fatal("mismatch accepted")
				}
				entries, _ := os.ReadDir(dir)
				if len(entries) != 0 {
					t.Fatalf("mismatch left files behind: %v", entries)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0o755 || filepath.Base(path) != "kind" {
				t.Fatalf("installed %s mode %v err %v", path, info.Mode(), err)
			}
		})
	}
}
