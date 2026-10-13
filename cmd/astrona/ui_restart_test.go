package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"astrona/internal/account"
	"astrona/internal/config"
	"astrona/internal/labstate"
	"github.com/spf13/cobra"
)

func uiRestartCommand(t *testing.T, lab string) (*cobra.Command, *rootFlags) {
	t.Helper()
	flags := &rootFlags{}
	root := newRootCmd(flags)
	cmd, _, err := root.Find([]string{"run"})
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.ParseFlags([]string{lab, "--ui"}); err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(t.Context())
	return cmd, flags
}

func restartState(t *testing.T, name, catalog string) *labstate.State {
	t.Helper()
	st := &labstate.State{Source: &labstate.Source{Catalog: catalog}, StartedAt: "2026-10-12T12:00:00Z", Session: &labstate.Session{ID: "old-session", Kind: "playground", Site: "https://astrona.example"}}
	if err := labstate.Save(name, st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestUIRestartMatchesRequestedLab(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(map[bool]string{true: "existing", false: "stale shell"}[exists], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(labShellEnvVar, "astro-other")
			lab := "ATS000/section-010/module-01/lab-01"
			restartState(t, "astro-demo", lab)
			restartState(t, "astro-other", "ATS111/section-010/module-01/lab-01")
			cmd, flags := uiRestartCommand(t, "astrona.io/"+lab)
			read := func(context.Context) (map[string][]kindContainer, error) {
				live := map[string][]kindContainer{"astro-other": {{state: "running"}}}
				if exists {
					live["astro-demo"] = []kindContainer{{state: "running"}}
				}
				return live, nil
			}
			restart, err := findUIRunRestart(cmd, flags, read)
			if err != nil || (restart != nil) != exists {
				t.Fatalf("restart=%+v err=%v", restart, err)
			}
			if restart != nil {
				if restart.name != "astro-demo" {
					t.Fatal("inherited shell selected a different lab")
				}
				flags.uiRestart, flags.localOnly = restart, true
				notice := buildUIRunNotice(cmd, flags, settingsView{}, false)
				for _, want := range []string{"astro-demo", "will be lost", "local only", "No cancels"} {
					if !strings.Contains(notice.summary, want) {
						t.Fatalf("missing %q", want)
					}
				}
				if err := approveUIRestart(cmd, flags, restart.name, func(*cobra.Command, uiRunNotice) (bool, error) {
					t.Fatal("asked twice about the same run")
					return false, nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestUIRestartConsentAndRunChanges(t *testing.T) {
	for _, mode := range []string{"decline", "accept", "changed before dialog", "changed during dialog"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			st := restartState(t, "astro-demo", "ATS000/lab")
			cmd, flags := uiRestartCommand(t, "ATS000/lab")
			before, err := labstate.Path("astro-demo")
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(before)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "changed before dialog" {
				flags.uiRestart = &uiRunRestart{"astro-demo", "another run"}
			}
			calls := 0
			err = approveUIRestart(cmd, flags, "astro-demo", func(*cobra.Command, uiRunNotice) (bool, error) {
				calls++
				if mode == "changed during dialog" {
					st.StartedAt = "2026-10-12T13:00:00Z"
					if err := labstate.Save("astro-demo", st); err != nil {
						t.Fatal(err)
					}
				}
				return mode != "decline", nil
			})
			switch mode {
			case "decline":
				if !errors.Is(err, errUIRunDeclined) || flags.localOnly {
					t.Fatal("decline changed the run")
				}
				after, err := os.ReadFile(before)
				if err != nil || !bytes.Equal(data, after) {
					t.Fatal("decline modified saved state")
				}
			case "changed before dialog":
				if err == nil || calls != 0 {
					t.Fatal("accepted a replacement of the approved run")
				}
			case "changed during dialog":
				if err != nil || discardUIRun("astro-demo", flags.uiRestart) == nil {
					t.Fatal("discard accepted a stale run")
				}
			case "accept":
				if err != nil || !flags.localOnly || calls != 1 {
					t.Fatal("restart consent missing")
				}
			}
		})
	}
}

func TestUIRestartNeverUsesAccountAPI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldDeps := accountDeps
	accountDeps = func() (*account.Client, account.Store, string, error) {
		t.Fatal("local restart accessed the account API or credentials")
		return nil, account.Store{}, "", errors.New("unexpected account access")
	}
	t.Cleanup(func() { accountDeps = oldDeps })
	st := restartState(t, "astro-demo", "ATS000/lab")
	approval := &uiRunRestart{"astro-demo", workspaceRunID(st)}
	if err := discardUIRun("astro-demo", approval); err != nil {
		t.Fatal(err)
	}
	flags := &rootFlags{localOnly: true, catalogLab: "ATS000/lab"}
	acct, err := accountForLab(t.Context(), flags)
	if err != nil || acct != nil {
		t.Fatal("local restart attempted account tracking")
	}
	start, err := startLabSession(t.Context(), acct, "run", &config.LabConfig{}, flags.catalogLab)
	if err != nil {
		t.Fatal(err)
	}
	start.progress("preparing", "test")
	start.abandon()
	rememberLab("astro-demo", flags, start.sess)
	start.done("astro-demo")
	state, err := labstate.Load("astro-demo")
	if err != nil || !state.LocalOnly || state.Session != nil {
		t.Fatal("local-only state was not preserved")
	}
	var out bytes.Buffer
	if sendResult(t.Context(), &out, "astro-demo", state.Source, submissionResult{}) || !strings.Contains(out.String(), "local only") {
		t.Fatal("local result was sent or not explained")
	}
	// Even a stale session cannot turn uploads or playground reporting back on.
	state.Session = st.Session
	if err := labstate.Save("astro-demo", state); err != nil {
		t.Fatal(err)
	}
	if sendResult(t.Context(), io.Discard, "astro-demo", state.Source, submissionResult{}) {
		t.Fatal("stale session allowed upload")
	}
	endRunSession("astro-demo")
	if err := watchPlayground(t.Context(), "astro-demo", time.Millisecond); err != nil {
		t.Fatal(err)
	}
}
