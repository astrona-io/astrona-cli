package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/config"
	"astrona/internal/gitsource"
	"astrona/internal/labstate"
	"astrona/internal/proctor"
)

func TestSubmitCleansUpAfterConfirmedResult(t *testing.T) {
	for _, tc := range []struct {
		name                                                 string
		pass, keep, keepCluster, json, deleteFails, replaced bool
		status                                               int
		deleted, wantError                                   bool
	}{
		{name: "uploaded pass", pass: true, status: 201, deleted: true},
		{name: "JSON uploaded pass", pass: true, json: true, status: 201, deleted: true},
		{name: "keep", pass: true, keep: true, status: 201},
		{name: "keepCluster", pass: true, keepCluster: true, status: 201},
		{name: "uploaded failure", status: 201, deleted: true, wantError: true},
		{name: "unsent failure", status: 502, wantError: true},
		{name: "server error", pass: true, status: 502},
		{name: "expired session", pass: true, status: 410},
		{name: "cleanup error", pass: true, status: 201, deleteFails: true, wantError: true},
		{name: "replaced during upload", pass: true, status: 201, replaced: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, calls := fakeResultSite(t, tc.status)
			sendSetup(t, srv, false, srv.URL)
			st, err := labstate.Load("astro-x")
			if err != nil {
				t.Fatal(err)
			}
			st.StartedAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
			st.KubeContext = &labstate.KubeContext{Lab: "kind-astro-x", Previous: "previous-cluster"}
			if err := labstate.Save("astro-x", st); err != nil {
				t.Fatal(err)
			}
			if tc.replaced {
				handler := srv.Config.Handler
				srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/results") {
						// Simulate another command starting a new run while upload waits.
						replacement := *st
						replacement.StartedAt = time.Now().UTC().Format(time.RFC3339)
						if err := labstate.Save("astro-x", &replacement); err != nil {
							t.Error(err)
						}
					}
					handler.ServeHTTP(w, r)
				})
			}
			bin := t.TempDir()
			log := filepath.Join(t.TempDir(), "deleted")
			t.Setenv("DESTROY_LOG", log)
			t.Setenv("KIND_EXPERIMENTAL_PROVIDER", "docker")
			for name, script := range map[string]string{
				"kubectl": "exit 98",
				"docker":  "if [ \"$1\" = ps ]; then echo astro-x-control-plane; fi",
				"kind":    fmt.Sprintf("printf '%%s\\n' \"$*\" >> \"$DESTROY_LOG\"; exit %d", map[bool]int{true: 1, false: 0}[tc.deleteFails]),
				"grade":   fmt.Sprintf("exit %d", map[bool]int{true: 0, false: 1}[tc.pass]),
			} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			cfg := &config.LabConfig{
				Metadata:   config.MetadataConfig{Name: "x"},
				Runtime:    config.RuntimeConfig{Type: "kind"},
				Teardown:   config.TeardownConfig{KeepCluster: tc.keepCluster},
				Validation: config.ValidationConfig{Checks: []config.ValidationCheck{{Name: "result", Type: "command", Command: "grade"}}},
			}
			options := submitOptions{keep: tc.keep}
			if tc.json {
				options.output = "json"
			}
			var status bytes.Buffer
			stdout := captureStdout(t, func() {
				err = submitLoadedLab(&rootFlags{catalogLab: "ATS/x", headless: true}, cfg, t.TempDir(), options, &status)
			})
			if (err != nil) != tc.wantError || calls.Load() != 1 {
				t.Fatalf("submission error=%v calls=%d; output=%s", err, calls.Load(), stdout)
			}
			if tc.json && !json.Valid([]byte(stdout)) {
				t.Fatalf("cleanup polluted JSON: %q", stdout)
			}
			deleted, _ := os.ReadFile(log)
			attempted := tc.deleted || tc.deleteFails
			if strings.Contains(string(deleted), "delete cluster --name astro-x") != attempted {
				t.Fatalf("unexpected deletion commands: %q", deleted)
			}
			current, err := labstate.Load("astro-x")
			if err != nil || (current == nil) != tc.deleted {
				t.Fatalf("saved run=%+v, err=%v", current, err)
			}
			if strings.Contains(status.String(), "deleted.") != tc.deleted {
				t.Fatalf("incorrect cleanup confirmation: %s", &status)
			}
			attempts, err := proctor.LoadAttempts("astro-x")
			if err != nil || len(attempts) != 1 || attempts[0].Pass != tc.pass {
				t.Fatalf("lost grading result: %+v %v", attempts, err)
			}
		})
	}
}

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
