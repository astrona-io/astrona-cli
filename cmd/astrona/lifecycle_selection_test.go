package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func TestLifecycleSelectionFailureDoesNotDiscover(t *testing.T) {
	for _, verb := range []string{"start", "stop", "submit"} {
		for _, tc := range []struct {
			name          string
			args          []string
			saved, config string
		}{
			{name: "missing config", args: []string{"-c", "missing"}},
			{name: "missing file", args: []string{"--file", "missing.yaml"}},
			{name: "explicit ref", args: []string{"--git-ref", "main"}},
			{name: "broken current selection", saved: "{"},
			{name: "missing current selection", saved: `{"config":"missing"}`},
			{name: "invalid local config", config: "metadata: ["},
		} {
			t.Run(verb+"/"+tc.name, func(t *testing.T) {
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
					if err := os.WriteFile(path, []byte(tc.saved), 0600); err != nil {
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
				root.SetArgs(append([]string{verb}, tc.args...))
				if err := root.Execute(); err == nil {
					t.Fatal("expected selected config error")
				}
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("runtime discovery ran after selected source failed: %v", err)
				}
			})
		}
	}
}
