package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/terms"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
)

type uiRunGateFixture struct {
	root   *cobra.Command
	flags  *rootFlags
	path   string
	cancel context.CancelFunc
	ran    bool
}

func newUIRunGateFixture(t *testing.T) *uiRunGateFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f := &uiRunGateFixture{path: filepath.Join(t.TempDir(), "settings.json")}
	if err := writeSettingsFile(f.path, userSettings{"update-check": []byte("false")}); err != nil {
		t.Fatal(err)
	}
	f.flags = &rootFlags{settingsFile: f.path}
	f.root = newRootCmd(f.flags)
	ctx, cancel := context.WithCancel(t.Context())
	f.cancel = cancel
	t.Cleanup(cancel)
	f.root.SetContext(ctx)
	f.root.SetOut(io.Discard)
	f.root.SetErr(io.Discard)
	run, _, err := f.root.Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	f.root.PersistentPreRunE = func(*cobra.Command, []string) error {
		if !f.flags.uiStartupApproved || f.flags.trust || !f.flags.installVersion {
			t.Fatal("startup consent not scoped to invocation")
		}
		return nil
	}
	run.RunE = func(*cobra.Command, []string) error { f.ran = true; return nil }
	installCommandGate(f.root, func(cmd *cobra.Command) error { return checkSettingsReview(cmd, f.flags) })
	installTermsGate(f.root, f.flags, func(cmd *cobra.Command, explicit bool) error {
		store, err := terms.DefaultStore()
		if err != nil {
			return err
		}
		return store.Ensure(cmd.Context(), strings.NewReader(""), io.Discard, false, false, explicit, Version)
	})
	f.root.SetArgs([]string{"run", "ATS000/section-010/module-01/lab-01", "--ui"})
	return f
}

func checkUIRunConsent(t *testing.T, f *uiRunGateFixture, accepted bool) {
	t.Helper()
	if accepted {
		view, err := buildSettingsView(f.path)
		if err != nil || len(view.Pending) != 0 {
			t.Fatalf("review missing: %+v %v", view, err)
		}
		saved, err := loadSettings(f.path)
		if err != nil || string(saved["update-check"]) != "false" {
			t.Fatal("saved preference replaced")
		}
		return
	}
	if f.flags.uiStartupApproved {
		t.Fatal("continued without consent")
	}
	if _, err := os.Stat(settingsReceiptPath(f.path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("review receipt written without consent")
	}
	store, err := terms.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	consent, err := store.Accepted()
	if err != nil || consent {
		t.Fatal("terms accepted without consent")
	}
}

func TestUIRunGate(t *testing.T) {
	for _, mode := range []string{"accept", "decline", "changed settings", "cancelled context"} {
		t.Run(mode, func(t *testing.T) {
			f := newUIRunGateFixture(t)
			confirmations := 0
			installUIRunGate(f.root, f.flags, func(_ *cobra.Command, notice uiRunNotice) (bool, error) {
				confirmations++
				for _, text := range []string{"ATS000/section-010/module-01/lab-01", "update-check=no", "Other labs stay running", "signed out", "this run only"} {
					if !strings.Contains(notice.summary, text) {
						t.Fatalf("notice missing %q", text)
					}
				}
				if !strings.Contains(notice.details, terms.Document()) {
					t.Fatal("full terms missing")
				}
				switch mode {
				case "changed settings":
					if err := writeSettingsFile(f.path, userSettings{"update-check": []byte("true")}); err != nil {
						t.Fatal(err)
					}
				case "cancelled context":
					f.cancel()
				}
				return mode != "decline", nil
			})
			err := f.root.Execute()
			accepted := mode == "accept"
			if confirmations != 1 || f.ran != accepted || (err == nil) != accepted {
				t.Fatalf("confirmations=%d ran=%v err=%v", confirmations, f.ran, err)
			}
			if mode == "decline" && !errors.Is(err, errUIRunDeclined) {
				t.Fatal(err)
			}
			checkUIRunConsent(t, f, accepted)
		})
	}
}

func TestUIRunCodeApprovalIsNotSaved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	flags := &rootFlags{uiStartupApproved: true}
	cfg := &config.LabConfig{SourcePath: "https://example.test/config.yaml", SourceSHA256: "abc"}
	if err := requireTrust(flags, cfg, dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("invocation approval wrote persistent trust")
	}
}

func TestUIRunGateScopeAndDefaults(t *testing.T) {
	for _, option := range []string{"--ui", "--ui-startup", "--ui=false"} {
		t.Run(option, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			path := filepath.Join(t.TempDir(), "settings.json")
			flags := &rootFlags{settingsFile: path}
			root := newRootCmd(flags)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
			run, _, err := root.Find([]string{"run"})
			if err != nil {
				t.Fatal(err)
			}
			run.RunE = func(*cobra.Command, []string) error { return nil }
			called := false
			installUIRunGate(root, flags, func(_ *cobra.Command, notice uiRunNotice) (bool, error) {
				called = true
				if !strings.Contains(notice.summary, "update-check=yes") || !strings.Contains(notice.summary, "current work will be deleted") {
					t.Fatal("default or requested restart missing")
				}
				return true, nil
			})
			root.SetArgs([]string{"run", "./lab", option, "--yes"})
			if err := root.Execute(); err != nil || called != (option != "--ui=false") {
				t.Fatalf("called=%v err=%v", called, err)
			}
		})
	}
}

func TestUIRunNoticeControls(t *testing.T) {
	for _, tt := range []struct {
		name   string
		keys   []tea.KeyType
		accept bool
	}{
		{"enter declines", []tea.KeyType{tea.KeyEnter}, false},
		{"escape declines", []tea.KeyType{tea.KeyEsc}, false},
		{"interrupt declines", []tea.KeyType{tea.KeyCtrlC}, false},
		{"select yes", []tea.KeyType{tea.KeyLeft, tea.KeyEnter}, true},
		{"details keeps no", []tea.KeyType{tea.KeyTab, tea.KeyEnter}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := uiRunModel{width: 80, height: 24}
			var command tea.Cmd
			for _, key := range tt.keys {
				next, cmd := m.Update(tea.KeyMsg{Type: key})
				m, command = next.(uiRunModel), cmd
			}
			if m.accepted != tt.accept || command == nil {
				t.Fatalf("accepted=%v quit=%v", m.accepted, command != nil)
			}
		})
	}
	for _, size := range [][2]int{{20, 8}, {48, 14}, {80, 24}, {140, 45}} {
		m := uiRunModel{width: size[0], height: size[1], notice: uiRunNotice{summary: strings.Repeat("long notice 界\x1b[2J\a\n", 50) + "last line"}}
		for _, key := range []tea.KeyType{tea.KeyEnd, tea.KeyPgUp, tea.KeyHome} {
			next, _ := m.Update(tea.KeyMsg{Type: key})
			m = next.(uiRunModel)
			view := ansi.Strip(m.View())
			if len(strings.Split(view, "\n")) > m.height || strings.ContainsAny(view, "\x1b\a") {
				t.Fatal("unsafe or overflowing notice")
			}
			for _, line := range strings.Split(view, "\n") {
				if ansi.StringWidth(line) > m.width {
					t.Fatal("notice exceeds terminal width")
				}
			}
			if key == tea.KeyEnd && size[0] >= 48 && !strings.Contains(view, "last line") {
				t.Fatal("cannot scroll to end")
			}
		}
		if size[0] < 48 {
			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
			if next.(uiRunModel).accepted || cmd != nil {
				t.Fatal("unreadable notice accepted")
			}
		}
	}
}
