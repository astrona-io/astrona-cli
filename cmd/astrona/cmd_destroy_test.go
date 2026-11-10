package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/catalog"
	"github.com/spf13/cobra"
)

func TestDestroySelectedSourceFailureDoesNotDiscover(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		selected bool
	}{
		{"missing config", []string{"-c", "missing"}, false},
		{"missing file", []string{"--file", "missing.yaml"}, false},
		{"failed git", []string{"--git", "https://example.invalid/lab.git"}, false},
		{"explicit ref", []string{"--git-ref", "main"}, false},
		{"saved selection", nil, true},
		{"invalid config", []string{"-c", "broken.yaml"}, false},
		{"catalog checkout failure", []string{"ATS014/section-010/module-01/lab-01"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			t.Chdir(t.TempDir())
			marker := filepath.Join(t.TempDir(), "runtime-called")
			t.Setenv("ASTRONA_TEST_CALLS", marker)
			bin := t.TempDir()
			for _, name := range []string{"kind", "docker", "podman", "kubectl"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf called >> \"$ASTRONA_TEST_CALLS\"\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			if err := os.WriteFile("broken.yaml", []byte("metadata: ["), 0600); err != nil {
				t.Fatal(err)
			}
			store, err := catalogStore()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(catalog.Catalog{FetchedAt: time.Now(), Trainings: []catalog.Training{{
				ID: "ATS014", Repo: "https://github.com/astrona-io/ATS014.git",
				Labs: []catalog.Lab{{ID: "ATS014/section-010/module-01/lab-01", Path: "missing"}},
			}}}); err != nil {
				t.Fatal(err)
			}
			flags := &rootFlags{}
			root := newRootCmd(flags)
			root.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
			if tc.selected {
				flags.fromCurrent, flags.configPath = true, "missing"
			}
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(append([]string{"destroy"}, tc.args...))
			err = root.Execute()
			if err == nil || !strings.Contains(err.Error(), "nothing was destroyed") {
				t.Fatalf("selected source failure = %v", err)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("runtime discovery ran after selected source failed: %v", err)
			}
		})
	}
}

func TestDestroySavedSelectionReadFailure(t *testing.T) {
	for _, tc := range []struct {
		name, saved, config string
		args                []string
		wantRuntime         bool
	}{
		{name: "malformed selection", saved: "{"},
		{name: "unreadable selection", saved: "directory"},
		{name: "malformed selection with invalid cwd config", saved: "{", config: "metadata: ["},
		{name: "unreadable selection with invalid cwd config", saved: "directory", config: "metadata: ["},
		{name: "saved selection with invalid cwd config", saved: `{"config":"missing"}`, config: "metadata: ["},
		{name: "valid cwd config wins", saved: "{", config: "metadata: {name: here}\n", wantRuntime: true},
		{name: "named target wins", saved: "{", args: []string{"astro-named"}, wantRuntime: true},
		{name: "no selection permits discovery", wantRuntime: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Chdir(t.TempDir())
			if tc.saved != "" {
				path, err := currentLabPath()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if tc.saved == "directory" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte(tc.saved), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.config != "" {
				if err := os.WriteFile("config.yaml", []byte(tc.config), 0600); err != nil {
					t.Fatal(err)
				}
			}
			marker := filepath.Join(t.TempDir(), "runtime-called")
			t.Setenv("ASTRONA_TEST_CALLS", marker)
			bin := t.TempDir()
			for _, name := range []string{"kind", "docker", "podman", "kubectl"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf called >> \"$ASTRONA_TEST_CALLS\"\nexit 1\n"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			flags := &rootFlags{}
			root := newRootCmd(flags)
			root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
				applyCurrentLab(cmd, flags)
				return nil
			}
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			root.SetArgs(append([]string{"destroy"}, tc.args...))
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a selection or runtime error")
			}
			if !tc.wantRuntime && !strings.Contains(err.Error(), "nothing was destroyed") {
				t.Fatalf("saved selection was ignored: %v", err)
			}
			_, statErr := os.Stat(marker)
			if tc.wantRuntime && statErr != nil {
				t.Fatalf("explicit target or unselected discovery did not reach the runtime: %v; command error: %v", statErr, err)
			}
			if !tc.wantRuntime && !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("runtime discovery ran after selection failure: %v", statErr)
			}
		})
	}
}
