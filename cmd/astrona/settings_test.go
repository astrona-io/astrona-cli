package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/spf13/cobra"
)

func executeSettings(t *testing.T, path string, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd(&rootFlags{settingsFile: path})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"settings"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestSettingsReadDoesNotCreateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".astrona", "settings.json")
	for _, args := range [][]string{nil, {"-o", "json"}, {"get", "parallel"}, {"path"}} {
		out, err := executeSettings(t, path, args...)
		if err != nil || out == "" {
			t.Fatalf("%v: %q, %v", args, out, err)
		}
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("read-only commands created a directory: %v", err)
	}
	got, err := executeSettings(t, path, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal([]byte(got), &values); err != nil {
		t.Fatalf("not one JSON document: %s, %v", got, err)
	}
	if len(values) != 6 || values["verbose"] != false || values["keep-context"] != false || values["parallel"] != float64(1) || values["update-check"] != true || values["cache-retention-days"] != float64(30) || values["cache-max-size-gb"] != float64(50) {
		t.Fatalf("unexpected defaults: %v", values)
	}
}

func TestSettingsSetGetUnset(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".astrona", "settings.json")
	for _, pair := range [][2]string{{"verbose", "true"}, {"keep-context", "true"}, {"parallel", "3"}, {"update-check", "false"}} {
		if _, err := executeSettings(t, path, "set", pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
		if got, err := executeSettings(t, path, "get", pair[0]); err != nil || strings.TrimSpace(got) != pair[1] {
			t.Fatalf("get %s: %q, %v", pair[0], got, err)
		}
	}
	for _, target := range []struct {
		path string
		mode os.FileMode
	}{{path, 0o600}, {filepath.Dir(path), 0o700}} {
		info, err := os.Stat(target.path)
		if err != nil || info.Mode().Perm() != target.mode {
			t.Fatalf("wrong permissions for %s: %v, %v", target.path, info, err)
		}
	}
	if _, err := executeSettings(t, path, "unset", "keep-context"); err != nil {
		t.Fatal(err)
	}
	got, err := executeSettings(t, path, "get", "keep-context")
	if err != nil || got != "false\n" {
		t.Fatalf("unset did not restore the default: %q, %v", got, err)
	}
	saved, err := loadSettings(path)
	if err != nil || len(saved) != 3 || string(saved["parallel"]) != "3" {
		t.Fatalf("unset changed other settings: %v, %v", saved, err)
	}
	if _, exists := saved["keep-context"]; exists {
		t.Fatal("unset must remove the override")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary file was left behind: %s", entry.Name())
		}
	}
}

func TestSettingsRejectInvalidEditsWithoutChangingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte("{\"verbose\": true}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"set", "verbose", "yes"}, {"set", "parallel", "0"}, {"set", "parallel", "6"},
		{"set", "parallel", "1.5"}, {"set", "trust", "true"}, {"unset", "unknown"},
		{"get", "unknown"}, {"set", "keep-context"}, {"set", "verbose", "false", "extra"},
		{"-o", "yaml"},
	} {
		if _, err := executeSettings(t, path, args...); err == nil {
			t.Fatalf("accepted invalid arguments: %v", args)
		}
		if data, err := os.ReadFile(path); err != nil || !bytes.Equal(data, original) {
			t.Fatalf("%v changed the file: %q, %v", args, data, err)
		}
	}
}

func TestSettingsRejectDamagedFiles(t *testing.T) {
	for _, data := range []string{
		"", "{broken", "null", "[]", "{} {}", `{"verbose":"true"}`, `{"verbose":null}`,
		`{"parallel":1.5}`, `{"parallel":0}`, `{"parallel":6}`, `{"trust":true}`,
		strings.Repeat(" ", 64*1024+1),
	} {
		t.Run(data[:min(len(data), 30)], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadSettings(path); err == nil {
				t.Fatal("accepted damaged file")
			}
			if _, err := executeSettings(t, path, "set", "verbose", "false"); err == nil {
				t.Fatal("overwrote damaged file")
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != data {
				t.Fatalf("damaged file was changed: %v", err)
			}
			if got, err := executeSettings(t, path, "path"); err != nil || got != path+"\n" {
				t.Fatalf("path must work for repair: %q, %v", got, err)
			}
		})
	}
}

func TestSettingsRejectSymlinksAndDirectories(t *testing.T) {
	for _, name := range []string{"settings.json", "settings.json.lock"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			other := filepath.Join(dir, "other.json")
			if err := os.WriteFile(other, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
			if _, err := executeSettings(t, filepath.Join(dir, "settings.json"), "set", "verbose", "true"); err == nil {
				t.Fatal("followed a symlink")
			}
			if got, err := os.ReadFile(other); err != nil || string(got) != "{}" {
				t.Fatalf("changed symlink target: %s, %v", got, err)
			}
		})
	}
	if _, err := loadSettings(t.TempDir()); err == nil {
		t.Fatal("accepted a directory")
	}
}

func TestSettingsConcurrentWriterRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSettings(t, path, "set", "verbose", "true"); err == nil || !strings.Contains(err.Error(), "lock settings") {
		t.Fatalf("concurrent update was not refused: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := executeSettings(t, path, "set", "verbose", "true"); err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
}

func TestSettingsApplyToCommandsAndRespectExplicitFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"verbose":true,"keep-context":true,"parallel":3,"update-check":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASTRONA_NO_UPDATE_CHECK", "1")
	for _, command := range []string{"run", "reset", "test", "list"} {
		for _, explicit := range []bool{false, true} {
			t.Run(command+map[bool]string{false: "/saved", true: "/explicit"}[explicit], func(t *testing.T) {
				flags := &rootFlags{settingsFile: path}
				root := newRootCmd(flags)
				cmd, _, err := root.Find([]string{command})
				if err != nil {
					t.Fatal(err)
				}
				called := false
				cmd.RunE = func(cmd *cobra.Command, args []string) error { called = true; return nil }
				args := []string{command}
				if explicit {
					args = append(args, "--verbose=false", "--update-check=true")
					if cmd.Flags().Lookup("parallel") != nil {
						args = append(args, "--parallel=1")
					}
					if cmd.Flags().Lookup("keep-context") != nil {
						args = append(args, "--keep-context=false")
					}
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil || !called {
					t.Fatalf("execute: %v, called=%v", err, called)
				}
				if flags.verbose != !explicit || flags.updateCheck != explicit {
					t.Fatalf("wrong persistent preferences: %+v", flags)
				}
				if cmd.Flags().Lookup("parallel") != nil {
					want := 3
					if explicit {
						want = 1
					}
					if flags.parallel != want {
						t.Fatalf("parallel=%d, want %d", flags.parallel, want)
					}
				}
				if cmd.Flags().Lookup("keep-context") != nil && flags.keepContext != !explicit {
					t.Fatalf("wrong context preference: %v", flags.keepContext)
				}
				if explicit && len(flags.settingsArgs) != 0 {
					t.Fatalf("saved flags would override explicit flags on hand-over: %v", flags.settingsArgs)
				}
			})
		}
	}
}

func TestSettingsInvalidFileStopsBeforeCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd(&rootFlags{settingsFile: path})
	cmd, _, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	cmd.RunE = func(cmd *cobra.Command, args []string) error { called = true; return nil }
	root.SetArgs([]string{"run"})
	if err := root.Execute(); err == nil || called {
		t.Fatalf("invalid settings did not stop command: %v, called=%v", err, called)
	}
}

func TestSettingsHandoverPreservesSavedFlags(t *testing.T) {
	flags := &rootFlags{configPath: ".", fileName: "config.yaml", settingsArgs: []string{"--keep-context=true", "--parallel=2"}}
	args := handoverArgs([]string{"run", "--update-check=false"}, flags)
	if strings.Contains(strings.Join(args, " "), "--update-check") {
		t.Fatal("update-check is local to this process and must not be forwarded")
	}
	// Execute the generated arguments against the real command tree without
	// starting a lab, including command-local flags before the command name.
	nextFlags := &rootFlags{settingsFile: filepath.Join(t.TempDir(), "settings.json")}
	root := newRootCmd(nextFlags)
	cmd, _, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error { return nil }
	t.Setenv("ASTRONA_NO_UPDATE_CHECK", "1")
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !nextFlags.keepContext || nextFlags.parallel != 2 {
		t.Fatalf("hand-over lost saved preferences: %+v", nextFlags)
	}
}

func TestSettingsCompletion(t *testing.T) {
	for _, action := range []string{"get", "set", "unset"} {
		cmd := &cobra.Command{Use: action}
		keys, directive := completeSetting(cmd, nil, "")
		if len(keys) != 6 || directive != cobra.ShellCompDirectiveNoFileComp {
			t.Fatalf("%s key completion: %v, %v", action, keys, directive)
		}
		values, _ := completeSetting(cmd, []string{"verbose"}, "")
		if action == "set" && strings.Join(values, " ") != "true false" {
			t.Fatalf("boolean completion: %v", values)
		}
		if action != "set" && len(values) != 0 {
			t.Fatalf("%s should not complete a value", action)
		}
	}
}

func TestSettingsPathDefaultsToAstronaDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := settingsPath(&rootFlags{})
	if err != nil || got != filepath.Join(home, ".astrona", "settings.json") {
		t.Fatalf("default path: %q, %v", got, err)
	}
}
