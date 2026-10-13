package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/account"
	"astrona/internal/config"
	"astrona/internal/version"

	"github.com/creack/pty"
	"github.com/spf13/cobra"
)

func TestRunTestModeOverridesUIBeforeGates(t *testing.T) {
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
						t.Fatalf("%s enabled in test mode", name)
					}
				}
				return nil
			}
			installUIRunGate(root, flags, func(*cobra.Command, uiRunNotice) (bool, error) {
				t.Fatal("test mode opened a UI notice")
				return false, nil
			})
			args := []string{"run", "./lab", "--test-mode"}
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
			} else if err != nil || !called || !ran || !flags.testMode {
				t.Fatalf("headless run: called=%v ran=%v err=%v", called, ran, err)
			}
		})
	}
}

func TestRunTestModeRequiresApprovalsOnTerminal(t *testing.T) {
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
	cmd, _, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{"--test-mode"}); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(t.Context())
	cmd.SetIn(terminal)
	cmd.SetErr(terminal)
	if settingsInteractive(cmd) {
		t.Fatal("test mode allows interactive settings")
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
}

func TestRunTestModeSkipsAccountAndGuestReminder(t *testing.T) {
	old := accountDeps
	t.Cleanup(func() { accountDeps = old })
	accountDeps = func() (*account.Client, account.Store, string, error) {
		t.Fatal("test mode accessed account credentials")
		return nil, account.Store{}, "", nil
	}
	flags := &rootFlags{testMode: true, catalogLab: "ATS000/section-010/module-01/lab-01"}
	acct, err := accountForLab(t.Context(), flags)
	if err != nil || acct != nil {
		t.Fatalf("account=%v err=%v", acct, err)
	}
	acct, approved, err := confirmGuestLabRun(&cobra.Command{}, flags, nil, false)
	if err != nil || acct != nil || !approved {
		t.Fatalf("guest approval=%v err=%v", approved, err)
	}
}

func TestRunTestModeVersionInstallRequiresApproval(t *testing.T) {
	old := stdinIsTerminal
	stdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { stdinIsTerminal = old })
	constraint, err := version.ParseConstraint(">=0.5.0")
	if err != nil {
		t.Fatal(err)
	}
	current, _ := version.Parse("0.4.0")
	wanted, _ := version.Parse("0.5.0")
	flags := &rootFlags{testMode: true, configPath: "./lab"}
	if _, err := offerInstall(constraint, current, wanted, flags); err == nil || !strings.Contains(err.Error(), "--install-version") {
		t.Fatalf("install approval bypassed: %v", err)
	}
	args := handoverArgs([]string{"run", "--test-mode"}, flags)
	if !strings.Contains(strings.Join(args, " "), "--test-mode") {
		t.Fatalf("handover lost test mode: %v", args)
	}
}
