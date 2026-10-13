package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/gitsource"
	"astrona/internal/labstate"
	"astrona/internal/proctor"
)

func TestSubmitSavedCatalogWithoutRefetchOrApproval(t *testing.T) {
	for _, pass := range []bool{false, true} {
		t.Run(fmt.Sprint(pass), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Setenv("KIND_EXPERIMENTAL_PROVIDER", "docker")
			bin := t.TempDir()
			for name, script := range map[string]string{
				"git":     "if [ \"$3\" = rev-parse ] && [ \"$4\" = HEAD ]; then echo abc123; else exit 99; fi",
				"docker":  "if [ \"$1\" = ps ]; then echo astro-offline-control-plane; fi",
				"kubectl": "exit 98",
				"grade":   fmt.Sprintf("exit %d", map[bool]int{true: 0, false: 1}[pass]),
			} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			src := &labstate.Source{Git: "https://github.com/astrona-io/ATS014.git", Config: "sections/section-010/module-01/labs/lab-01", Catalog: "ATS014/section-010/module-01/lab-01"}
			repo, err := gitsource.CachedGitConfigSource(src.Git, "")
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(repo, src.Config)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			body := "metadata: {name: offline}\nruntime: {type: kind}\nvalidation:\n  checks:\n    - {name: result, type: command, command: grade}\n"
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			st := &labstate.State{Source: src, StartedAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), LocalOnly: true}
			if err := labstate.Save("astro-offline", st); err != nil {
				t.Fatal(err)
			}
			// Startup's Yes must cover a later CLI invocation at this revision.
			flags := &rootFlags{uiStartupApproved: true}
			cfg, base, cleanup, err := loadSavedLab("astro-offline", st, flags)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if err := requireTrust(flags, cfg, base); err != nil {
				t.Fatal(err)
			}
			root := newRootCmd(&rootFlags{})
			root.SetArgs([]string{"submit", "astrona-io/" + src.Catalog, "--headless", "--keep"})
			out := captureStdout(t, func() {
				err = root.Execute()
			})
			if pass && err != nil || !pass && (err == nil || exitCodeFor(err) != exitNotPassed) {
				t.Fatalf("submission pass=%v: %v; output=%s", pass, err, out)
			}
			for _, unwanted := range []string{"Checking https", "First time", "Run it?"} {
				if strings.Contains(out, unwanted) {
					t.Fatalf("submission repeated startup: %s", out)
				}
			}
			attempts, err := proctor.LoadAttempts("astro-offline")
			if err != nil || len(attempts) != 1 || attempts[0].Pass != pass {
				t.Fatalf("attempts=%v err=%v", attempts, err)
			}
			finished, err := labstate.ReadSubmission("astro-offline", st.StartedAt)
			if err != nil || finished == nil || finished.Pass != pass {
				t.Fatalf("completion=%v err=%v", finished, err)
			}
		})
	}
}

func TestSavedSubmissionMissingCacheDoesNotFetch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	src := &labstate.Source{Git: "https://github.com/astrona-io/ATS014.git", Config: "missing", Catalog: "ATS014/section-010/module-01/lab-01"}
	if err := labstate.Save("astro-missing", &labstate.State{Source: src}); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd(&rootFlags{})
	root.SetArgs([]string{"submit", src.Catalog, "--headless"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "restore the saved config") {
		t.Fatalf("missing cache silently fell back to downloads: %v", err)
	}
}
