package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/account"
	"astrona/internal/config"
	"astrona/internal/version"

	"github.com/creack/pty"
	"github.com/spf13/cobra"
)

func TestHeadlessOverridesUIBeforeGates(t *testing.T) {
	for _, option := range []string{"", "--ui=false", "--ui", "--ui-startup"} {
		t.Run(option, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			flags := &rootFlags{}
			root := newRootCmd(flags)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			called := false
			root.PersistentPreRunE = func(*cobra.Command, []string) error { called = true; return nil }
			run, _, err := root.Find([]string{"run"})
			if err != nil {
				t.Fatal(err)
			}
			// Model the future UI default without marking it as explicitly requested.
			if err := run.Flags().Lookup("ui").Value.Set("true"); err != nil {
				t.Fatal(err)
			}
			ran := false
			run.RunE = func(cmd *cobra.Command, _ []string) error {
				ran = true
				for _, name := range []string{"ui", "ui-startup"} {
					if enabled, _ := cmd.Flags().GetBool(name); enabled {
						t.Fatalf("%s enabled in headless mode", name)
					}
				}
				return nil
			}
			installUIRunGate(root, flags, func(*cobra.Command, uiRunNotice) (bool, error) {
				t.Fatal("headless mode opened a UI notice")
				return false, nil
			})
			args := []string{"run", "./lab", "--headless"}
			if option != "" {
				args = append(args, option)
			}
			root.SetArgs(args)
			err = root.Execute()
			conflict := option == "--ui" || option == "--ui-startup"
			if conflict {
				if err == nil || !strings.Contains(err.Error(), "cannot be combined") || called || ran {
					t.Fatalf("conflict reached setup: called=%v ran=%v err=%v", called, ran, err)
				}
			} else if err != nil || !called || !ran || !flags.headless {
				t.Fatalf("headless run: called=%v ran=%v err=%v", called, ran, err)
			}
		})
	}
}

func TestHeadlessRequiresApprovalsOnTerminal(t *testing.T) {
	for _, command := range []string{"run", "reset", "test", "submit", "validate"} {
		t.Run(command, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("CI", "")
			t.Setenv("GITHUB_ACTIONS", "")
			master, terminal, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer terminal.Close()
			// An accidental interactive prompt must fail on EOF rather than hang the test.
			if _, err := master.Write([]byte{4}); err != nil {
				t.Fatal(err)
			}
			flags := &rootFlags{settingsFile: filepath.Join(t.TempDir(), "settings.json")}
			root := newRootCmd(flags)
			cmd, _, err := root.Find([]string{command})
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.ParseFlags([]string{"--headless"}); err != nil {
				t.Fatal(err)
			}
			cmd.SetContext(t.Context())
			cmd.SetIn(terminal)
			cmd.SetErr(terminal)
			if settingsInteractive(cmd) {
				t.Fatal("headless mode allows interactive settings")
			}
			if err := checkTerms(cmd, false); err == nil || !strings.Contains(err.Error(), "--accept-terms") {
				t.Fatalf("terms approval bypassed: %v", err)
			}
			if err := checkTerms(cmd, true); err != nil {
				t.Fatal(err)
			}
			if err := checkSettingsReview(cmd, flags); err == nil || !strings.Contains(err.Error(), "settings review") {
				t.Fatalf("settings approval bypassed: %v", err)
			}
			cfg := &config.LabConfig{SourcePath: "https://example.invalid/config.yaml", SourceSHA256: "abc"}
			if err := requireTrust(flags, cfg, t.TempDir()); err == nil || !strings.Contains(err.Error(), "--trust") {
				t.Fatalf("remote trust bypassed: %v", err)
			}
			flags.trust = true
			if err := requireTrust(flags, cfg, t.TempDir()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHeadlessSkipsAccountAndGuestReminder(t *testing.T) {
	old := accountDeps
	t.Cleanup(func() { accountDeps = old })
	accountDeps = func() (*account.Client, account.Store, string, error) {
		t.Fatal("headless mode accessed account credentials")
		return nil, account.Store{}, "", nil
	}
	flags := &rootFlags{headless: true, catalogLab: "ATS000/section-010/module-01/lab-01"}
	acct, err := accountForLab(t.Context(), flags)
	if err != nil || acct != nil {
		t.Fatalf("account=%v err=%v", acct, err)
	}
	acct, approved, err := confirmGuestLabRun(&cobra.Command{}, flags, nil, false)
	if err != nil || acct != nil || !approved {
		t.Fatalf("guest approval=%v err=%v", approved, err)
	}
}

func TestHeadlessVersionInstallRequiresApproval(t *testing.T) {
	old := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = old })
	constraint, err := version.ParseConstraint(">=0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	current, _ := version.Parse("0.4.0")
	wanted, _ := version.Parse("0.5.0")
	flags := &rootFlags{headless: true, configPath: "./lab"}
	if _, err := offerInstall(constraint, current, wanted, flags); err == nil || !strings.Contains(err.Error(), "--install-version") {
		t.Fatalf("install approval bypassed: %v", err)
	}
	args := handoverArgs([]string{"run", "--headless"}, flags)
	if !strings.Contains(strings.Join(args, " "), "--headless") {
		t.Fatalf("handover lost headless mode: %v", args)
	}
}

func TestHeadlessSharedFlagAndInteractiveConflicts(t *testing.T) {
	for _, command := range [][]string{
		{"run"}, {"reset"}, {"test"}, {"validate"}, {"check"}, {"submit"},
		{"destroy"}, {"stop"}, {"start"}, {"diagnose"}, {"doctor"}, {"list"},
		{"status"}, {"progress"}, {"bundle", "load"}, {"res", "run"},
		{"settings", "review"}, {"data", "clean"}, {"setup"}, {"login"},
		{"ui"}, {"shell"}, {"ssh"},
	} {
		t.Run(strings.Join(command, " "), func(t *testing.T) {
			flags := &rootFlags{}
			root := newRootCmd(flags)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
			cmd, _, err := root.Find(command)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Args = cobra.ArbitraryArgs
			ran := false
			cmd.RunE = func(cmd *cobra.Command, _ []string) error {
				ran = true
				if !flags.headless || !commandHeadless(cmd) {
					t.Fatal("headless flag not inherited")
				}
				return nil
			}
			installUIRunGate(root, flags, func(*cobra.Command, uiRunNotice) (bool, error) {
				t.Fatal("headless command opened a notice")
				return false, nil
			})
			root.SetArgs(append([]string{"--headless"}, command...))
			err = root.Execute()
			conflict := command[0] == "ui" || command[0] == "shell" || command[0] == "ssh"
			if conflict {
				if err == nil || ran || !strings.Contains(err.Error(), "interactive session") {
					t.Fatalf("interactive conflict: ran=%v err=%v", ran, err)
				}
			} else if err != nil || !ran {
				t.Fatalf("headless command failed: ran=%v err=%v", ran, err)
			}
		})
	}
}

func TestHeadlessSetupAndBundleRequireApproval(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := newRootCmd(&rootFlags{})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
	root.SetArgs([]string{"setup", "--headless"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("setup approval bypassed: %v", err)
	}
	flags := &rootFlags{headless: true}
	if err := trustBundle(flags, "lab.tar", "abc", nil, nil); err == nil || !strings.Contains(err.Error(), "--trust") {
		t.Fatalf("bundle approval bypassed: %v", err)
	}
	flags.trust = true
	if err := trustBundle(flags, "lab.tar", "abc", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestHeadlessLoginDoesNotOpenBrowser(t *testing.T) {
	srv := fakeAstrona(t, http.StatusCreated, nil)
	_, _ = withAccount(t, srv.URL)
	browserOpener = func(string) bool { t.Fatal("headless login opened a browser"); return false }
	root := newRootCmd(&rootFlags{})
	root.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
	root.SetArgs([]string{"login", "--headless"})
	out := captureStdout(t, func() {
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "To sign in, open "+srv.URL) || !strings.Contains(out, "Signed in as") || strings.Contains(out, "Opening your browser") {
		t.Fatalf("headless login output: %s", out)
	}
}

func TestHeadlessReplacesTestModeFlag(t *testing.T) {
	root := newRootCmd(&rootFlags{})
	cmd, _, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{"--test-mode"}); err == nil || !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("old flag still accepted: %v", err)
	}
}

func TestHeadlessResetRequiresYesOnTerminal(t *testing.T) {
	for _, mode := range [][]string{nil, {"--soft"}, {"--cluster", "idp"}, {"--soft", "--cluster", "idp"}} {
		t.Run(strings.Join(mode, " "), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			dir := t.TempDir()
			cfg := "metadata: {name: headless-reset}\nruntime:\n  type: kind\n  kind:\n    clusters:\n      - name: idp\n"
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0600); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			script := "#!/bin/sh\ncase \"$1\" in info) exit 0;; ps) printf 'astro-headless-reset-control-plane\\n';; *) exit 98;; esac\n"
			if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			master, terminal, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer master.Close()
			defer terminal.Close()
			previous := os.Stdin
			os.Stdin = terminal
			defer func() { os.Stdin = previous }()
			if _, err := master.Write([]byte{4}); err != nil {
				t.Fatal(err)
			}
			root := newRootCmd(&rootFlags{})
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
			root.SetArgs(append([]string{"reset", "-c", dir, "--headless"}, mode...))
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--yes") {
				t.Fatalf("reset did not require explicit approval: %v", err)
			}
		})
	}
}
