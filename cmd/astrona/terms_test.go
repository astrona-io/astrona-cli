package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/terms"
	"github.com/spf13/cobra"
)

func TestTermsGateRunsBeforeCommandHooks(t *testing.T) {
	for _, args := range [][]string{{"run", "--yes", "--trust"}, {"run", "--accept-terms=false"}, {"run", "--accept-terms"}, {"--accept-terms", "run"}, {}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			flags := &rootFlags{}
			root := newRootCmd(flags)
			want := errors.New("gate refused")
			called := false
			for _, c := range append(root.Commands(), root) {
				c.PersistentPreRunE = func(*cobra.Command, []string) error { t.Fatal("setup ran before consent"); return nil }
				c.RunE = func(*cobra.Command, []string) error { t.Fatal("command ran before consent"); return nil }
			}
			installTermsGate(root, flags, func(_ *cobra.Command, explicit bool) error {
				called = true
				if explicit != strings.Contains(strings.Join(args, " ")+" ", "--accept-terms ") {
					t.Errorf("explicit = %v", explicit)
				}
				return want
			})
			root.SetArgs(args)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			_, provider := dataTestStore(t)
			if err := executeWithDataLock(root, provider); !called || !errors.Is(err, want) {
				t.Fatalf("called=%v, error=%v", called, err)
			}
		})
	}
}

func TestTermsExemptionsAndAliases(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"run", "--help"}, {"--version"}, {"help", "run"}, {"terms"}, {"completion", "bash"}, {"settings", "path"}, {"data", "reset"}, {"stop"}, {"destroy"}, {"logout"}, {"port-forward", "stop"}, {"docgen"}, {"schema"}, {"playground-watchdog", "astro-test"}, {"port-forward", "supervise", "lab", "name"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			flags := &rootFlags{}
			root := newRootCmd(flags)
			// Leave help/version generation intact; stub operations to keep the
			// exemption test from touching the caller's machine or network.
			var stub func(*cobra.Command)
			stub = func(c *cobra.Command) {
				c.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
				c.PreRunE = nil
				c.RunE = func(*cobra.Command, []string) error { return nil }
				c.Args = cobra.ArbitraryArgs
				for _, child := range c.Commands() {
					stub(child)
				}
			}
			stub(root)
			installTermsGate(root, flags, func(*cobra.Command, bool) error { t.Fatal("exempt command gated"); return nil })
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
		})
	}
	root := newRootCmd(&rootFlags{})
	for _, path := range [][]string{{"run"}, {"res", "run"}, {"reset"}, {"port-forward", "start"}, {"versions", "install"}} {
		cmd, _, err := root.Find(path)
		if err != nil || termsExempt(cmd) {
			t.Fatalf("operational command exempt: %v, %v", path, err)
		}
	}
}

func TestTermsOutputAndJSONSeparation(t *testing.T) {
	flags := &rootFlags{}
	root := newRootCmd(flags)
	s, provider := dataTestStore(t)
	store := terms.Store{Dir: s.Data}
	var out, notices bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&notices)
	installTermsGate(root, flags, func(c *cobra.Command, explicit bool) error {
		return store.Ensure(c.Context(), strings.NewReader(""), c.ErrOrStderr(), false, true, explicit, "test")
	})
	root.SetArgs([]string{"terms"})
	if err := executeWithDataLock(root, provider); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), terms.Document()) || notices.Len() != 0 {
		t.Fatal("terms read did not show full document")
	}
	if _, err := os.Stat(s.Data); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reading terms created state")
	}

	flags = &rootFlags{}
	root = newRootCmd(flags)
	cmd, _, _ := root.Find([]string{"status"})
	cmd.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		_, err := fmt.Fprintln(c.OutOrStdout(), `{"ok":true}`)
		return err
	}
	out.Reset()
	root.SetOut(&out)
	root.SetErr(&notices)
	root.SetArgs([]string{"status", "-o", "json", "--accept-terms"})
	installTermsGate(root, flags, func(c *cobra.Command, explicit bool) error {
		return store.Ensure(c.Context(), nil, c.ErrOrStderr(), false, true, explicit, "test")
	})
	if err := executeWithDataLock(root, provider); err != nil {
		t.Fatal(err)
	}
	if out.String() != "{\"ok\":true}\n" || !strings.Contains(notices.String(), "Accepted") {
		t.Fatalf("stdout=%q stderr=%q", out.String(), notices.String())
	}
	if _, err := os.Stat(filepath.Join(s.Data, "terms.json")); err != nil {
		t.Fatal(err)
	}
}

func TestHandoverDoesNotAcceptAnotherVersionsTerms(t *testing.T) {
	for _, flag := range []string{"--accept-terms", "--accept-terms=true", "--accept-terms=false"} {
		got := handoverArgs([]string{"run", flag}, &rootFlags{configPath: ".", fileName: "config.yaml"})
		if strings.Contains(strings.Join(got, " "), "--accept-terms") {
			t.Fatalf("forwarded consent: %v", got)
		}
	}
	got := handoverArgs([]string{"shell", "--", "echo", "--accept-terms"}, &rootFlags{})
	if !strings.Contains(strings.Join(got, " "), "-- echo --accept-terms") {
		t.Fatalf("changed positional argument: %v", got)
	}
}

func TestTermsExplicitAcceptanceIgnoresBrokenSettings(t *testing.T) {
	s, provider := dataTestStore(t)
	if err := os.MkdirAll(s.Data, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(s.Data, "settings.json")
	if err := os.WriteFile(settings, []byte("broken settings"), 0o600); err != nil {
		t.Fatal(err)
	}
	flags := &rootFlags{settingsFile: settings}
	root := newRootCmd(flags)
	store := terms.Store{Dir: s.Data}
	installTermsGate(root, flags, func(c *cobra.Command, explicit bool) error {
		return store.Ensure(c.Context(), nil, c.ErrOrStderr(), false, true, explicit, "test")
	})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"terms", "--accept-terms"})
	if err := executeWithDataLock(root, provider); err != nil {
		t.Fatal(err)
	}
	if accepted, err := store.Accepted(); !accepted || err != nil {
		t.Fatalf("accepted=%v err=%v", accepted, err)
	}
	data, err := os.ReadFile(settings)
	if err != nil || string(data) != "broken settings" {
		t.Fatalf("settings changed: %s, %v", data, err)
	}
}
