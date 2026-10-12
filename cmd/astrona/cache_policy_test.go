package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/localdata"
	"github.com/spf13/cobra"
)

func oldCacheFile(t *testing.T, s localdata.Store) string {
	t.Helper()
	path := filepath.Join(s.Data, "cache/addons/old.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("cached"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCacheSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	policy, err := loadCachePolicy(path)
	if err != nil || policy.MaxAge != 30*24*time.Hour || policy.MaxBytes != 50_000_000_000 {
		t.Fatalf("defaults=%+v, %v", policy, err)
	}
	for _, pair := range [][2]string{{"cache-retention-days", "60"}, {"cache-max-size-gb", "0"}} {
		if _, err := executeSettings(t, path, "set", pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
		if got, err := executeSettings(t, path, "get", pair[0]); err != nil || strings.TrimSpace(got) != pair[1] {
			t.Fatalf("get=%s, %v", got, err)
		}
		values, _ := completeSetting(&cobra.Command{Use: "set"}, []string{pair[0]}, "")
		if len(values) != 0 {
			t.Fatal("integer setting completed with booleans")
		}
	}
	policy, err = loadCachePolicy(path)
	if err != nil || policy.MaxAge != 60*24*time.Hour || policy.MaxBytes != 0 {
		t.Fatalf("saved=%+v, %v", policy, err)
	}
	for _, pair := range [][2]string{{"cache-retention-days", "0"}, {"cache-retention-days", "36501"}, {"cache-retention-days", "1.5"}, {"cache-max-size-gb", "-1"}, {"cache-max-size-gb", "50GB"}, {"cache-max-size-gb", "9223372037"}, {"cache-max-size-gb", "99999999999999999999"}} {
		if _, err := executeSettings(t, path, "set", pair[0], pair[1]); err == nil {
			t.Fatalf("accepted %v", pair)
		}
	}
	if _, err := executeSettings(t, path, "unset", "cache-max-size-gb"); err != nil {
		t.Fatal(err)
	}
	policy, err = loadCachePolicy(path)
	if err != nil || policy.MaxBytes != 50_000_000_000 {
		t.Fatalf("unset=%+v, %v", policy, err)
	}
}

func TestAutomaticCacheCleanupAfterCommandAndLockRelease(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s, provider := dataTestStore(t)
		path := oldCacheFile(t, s)
		want := errors.New("command failed")
		root := &cobra.Command{Use: "astrona", SilenceErrors: true, SilenceUsage: true, RunE: func(*cobra.Command, []string) error {
			if _, err := os.Stat(path); err != nil {
				t.Fatal("cache deleted during command")
			}
			if release, err := s.Lock(true); err == nil {
				release()
				t.Fatal("command had no shared lock")
			}
			if fail {
				return want
			}
			return nil
		}}
		var out, notice bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&notice)
		root.SetArgs([]string{})
		err := executeWithDataLock(root, provider)
		if (fail && !errors.Is(err, want)) || (!fail && err != nil) {
			t.Fatalf("result changed: %v", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old cache kept: %v", err)
		}
		if out.Len() != 0 || !strings.Contains(notice.String(), "removed 1") {
			t.Fatalf("out=%q, notice=%q", out.String(), notice.String())
		}
	}
}

func TestAutomaticCleanupDefersForOtherCommandsAndBadSettings(t *testing.T) {
	for _, cause := range []string{"lock", "settings", "state"} {
		t.Run(cause, func(t *testing.T) {
			s, _ := dataTestStore(t)
			path := oldCacheFile(t, s)
			switch cause {
			case "lock":
				release, err := s.Lock(false)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			case "settings":
				if err := os.WriteFile(filepath.Join(s.Data, "settings.json"), []byte("broken"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "state":
				if err := os.MkdirAll(filepath.Join(s.Data, "qemu/stopped"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			cmd := &cobra.Command{Use: "run"}
			cmd.SetContext(t.Context())
			var out bytes.Buffer
			cmd.SetErr(&out)
			enforceCachePolicy(cmd, s)
			if _, err := os.Stat(path); err != nil || !strings.Contains(out.String(), "deferred") {
				t.Fatalf("not safely deferred: %v, %q", err, out.String())
			}
		})
	}
}

func TestCachePolicyPreviewDryRunAndApply(t *testing.T) {
	for _, extra := range [][]string{nil, {"--yes", "--dry-run"}, {"--yes"}} {
		s, provider := dataTestStore(t)
		path := oldCacheFile(t, s)
		root := dataCommand(provider)
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(io.Discard)
		root.SetArgs(append([]string{"clean", "--policy", "-o", "json"}, extra...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		var plan localdata.Plan
		if err := json.Unmarshal(out.Bytes(), &plan); err != nil || len(plan.Items) != 1 {
			t.Fatalf("plan=%s, %v", out.String(), err)
		}
		apply := len(extra) == 1
		_, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) != apply || plan.Applied != apply {
			t.Fatalf("applied=%v, stat=%v", plan.Applied, err)
		}
	}
}

func TestCacheCleanupExemptCommands(t *testing.T) {
	root := newRootCmd(&rootFlags{})
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	for _, args := range [][]string{{"settings", "set"}, {"data", "clean"}, {"help"}, {"completion", "bash"}, {"docgen"}, {"playground-watchdog"}} {
		cmd, _, err := root.Find(args)
		if err != nil || cacheCleanupWanted(cmd) {
			t.Fatalf("unexpected cleanup for %v: %v", args, err)
		}
	}
}

func TestAutomaticCleanupDoesNotRunAfterSetupFailureOrPanic(t *testing.T) {
	for _, panicInRun := range []bool{false, true} {
		s, provider := dataTestStore(t)
		path := oldCacheFile(t, s)
		root := &cobra.Command{Use: "astrona", SilenceUsage: true, SilenceErrors: true}
		root.PersistentPreRunE = func(*cobra.Command, []string) error {
			if !panicInRun {
				return errors.New("setup refused")
			}
			return nil
		}
		root.RunE = func(*cobra.Command, []string) error { panic("unexpected failure") }
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs([]string{})
		func() {
			defer func() {
				if r := recover(); (r != nil) != panicInRun {
					t.Fatalf("panic=%v", r)
				}
			}()
			if err := executeWithDataLock(root, provider); err == nil {
				t.Fatal("setup unexpectedly succeeded")
			}
		}()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cache deleted on failed setup/panic: %v", err)
		}
		release, err := s.Lock(true)
		if err != nil {
			t.Fatal("maintenance lock leaked:", err)
		}
		release()
	}
}

func TestCachePolicyManualUsesSavedSettingsAndRejectsBadFlags(t *testing.T) {
	for _, tc := range []struct {
		settings string
		args     []string
		wantErr  bool
	}{
		{`{"cache-retention-days":60,"cache-max-size-gb":0}`, []string{"clean", "--policy", "--yes"}, false},
		{`broken`, []string{"clean", "--policy", "--yes"}, true},
		{`{}`, []string{"clean", "--policy", "--temp-only", "--yes"}, true},
	} {
		s, provider := dataTestStore(t)
		path := oldCacheFile(t, s)
		if err := os.WriteFile(filepath.Join(s.Data, "settings.json"), []byte(tc.settings), 0o600); err != nil {
			t.Fatal(err)
		}
		root := dataCommand(provider)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(tc.args)
		if err := root.Execute(); (err != nil) != tc.wantErr {
			t.Fatalf("error=%v for %v", err, tc.args)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("unexpected deletion:", err)
		}
	}
}

func TestCacheSettingsAreNotForwardedAsLabFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"cache-retention-days":60,"cache-max-size-gb":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := &rootFlags{settingsFile: path}
	root := newRootCmd(flags)
	cmd, _, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyUserSettings(cmd, flags); err != nil {
		t.Fatal(err)
	}
	if len(flags.settingsArgs) != 0 {
		t.Fatalf("cache preferences became lab flags: %v", flags.settingsArgs)
	}
}
