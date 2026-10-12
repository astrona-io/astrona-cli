package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/localdata"
	"github.com/spf13/cobra"
)

func dataTestStore(t *testing.T) (localdata.Store, func() (localdata.Store, error)) {
	t.Helper()
	base := t.TempDir()
	s := localdata.Store{Data: filepath.Join(base, ".astrona"), Repos: filepath.Join(base, "cache", "astrona", "repos")}
	return s, func() (localdata.Store, error) { return s, nil }
}

func TestDataCommandsPreviewAndConfirm(t *testing.T) {
	for _, args := range [][]string{{"reset"}, {"reset", "--dry-run", "--yes"}, {"reset", "--yes"}, {"clean", "--yes", "--temp-only"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			s, provider := dataTestStore(t)
			if err := os.MkdirAll(s.Data, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.Data, "settings.json")
			if err := os.WriteFile(path, []byte("malformed"), 0o600); err != nil {
				t.Fatal(err)
			}
			root := &cobra.Command{Use: "astrona", PersistentPreRunE: func(*cobra.Command, []string) error { t.Fatal("root settings/network hook ran"); return nil }}
			root.AddCommand(dataCommand(provider))
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(append(append([]string{"data"}, args...), "-o", "json"))
			if err := executeWithDataLock(root, provider); err != nil {
				t.Fatal(err)
			}
			var p localdata.Plan
			if err := json.Unmarshal(out.Bytes(), &p); err != nil {
				t.Fatalf("JSON output: %s: %v", out.String(), err)
			}
			_, err := os.Stat(path)
			removed := len(args) == 2 && args[0] == "reset" && args[1] == "--yes"
			if removed != errors.Is(err, os.ErrNotExist) {
				t.Fatalf("removed = %v; stat error %v", removed, err)
			}
			if removed && (!p.Applied || p.Removed != 1) {
				t.Fatalf("result = %+v", p)
			}
		})
	}
}

func TestDataUsageNoWritesAndInvalidFlags(t *testing.T) {
	for _, args := range [][]string{{"-o", "json"}, {"--output", "invalid"}, {"reset", "unexpected"}, {"clean", "--output", "invalid", "--yes"}} {
		s, provider := dataTestStore(t)
		cmd := dataCommand(provider)
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		if args[0] == "-o" {
			if err != nil || strings.TrimSpace(out.String()) != "[]" {
				t.Fatalf("%q, %v", out.String(), err)
			}
		} else if err == nil {
			t.Fatalf("accepted bad args: %v", args)
		}
		if _, err := os.Stat(s.Data); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("read-only command created data directory: %v", err)
		}
	}
}

func TestCommandLockCoversSetupAndReleasesOnError(t *testing.T) {
	s, provider := dataTestStore(t)
	want := errors.New("command failed")
	checked := false
	root := &cobra.Command{
		Use: "astrona",
		PersistentPreRunE: func(*cobra.Command, []string) error {
			if release, err := s.Lock(true); err == nil {
				release()
				t.Fatal("setup ran without shared lock")
			}
			checked = true
			return want
		},
		RunE: func(*cobra.Command, []string) error { t.Fatal("ran after setup failed"); return nil },
	}
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{})
	if err := executeWithDataLock(root, provider); !errors.Is(err, want) || !checked {
		t.Fatalf("execute = %v", err)
	}
	release, err := s.Lock(true)
	if err != nil {
		t.Fatalf("lock leaked: %v", err)
	}
	release()
}

func TestConfirmedCleanRefusesConcurrentCommand(t *testing.T) {
	s, provider := dataTestStore(t)
	release, err := s.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cmd := dataCommand(provider)
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"clean", "--yes"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("cleanup ignored running command")
	}
}
