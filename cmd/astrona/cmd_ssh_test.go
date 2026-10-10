package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSSHCmd(t *testing.T) {
	cmd := newSSHCmd()

	if cmd.Use != "ssh <lab-name>" {
		t.Errorf("expected command Use 'ssh <lab-name>', got %q", cmd.Use)
	}

	userFlag := cmd.Flag("user")
	if userFlag == nil {
		t.Error("expected '--user' flag to be defined")
	} else if userFlag.Value.Type() != "string" {
		t.Errorf("expected '--user' flag to be string, got %s", userFlag.Value.Type())
	}

	passwordFlag := cmd.Flag("password")
	if passwordFlag == nil {
		t.Error("expected '--password' flag to be defined")
	} else if passwordFlag.Value.Type() != "string" {
		t.Errorf("expected '--password' flag to be string, got %s", passwordFlag.Value.Type())
	}

	askFlag := cmd.Flag("ask-password")
	if askFlag == nil || askFlag.Value.Type() != "bool" {
		t.Errorf("expected a bool '--ask-password' flag, got %+v", askFlag)
	}
}

// The password flags fail before anything runs: --ask-password needs a
// terminal for ssh's prompt, the two can't be combined, and the
// argv-exposing --password is flagged as deprecated.
func TestSSHPasswordFlags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no ssh: nothing real can run

	// Stdin is a regular file, never a terminal.
	notTTY, err := os.Create(filepath.Join(t.TempDir(), "stdin"))
	if err != nil {
		t.Fatal(err)
	}
	defer notTTY.Close()
	oldStdin := os.Stdin
	os.Stdin = notTTY
	defer func() { os.Stdin = oldStdin }()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"ask-password without a terminal", []string{"ssh", "lab", "--ask-password"}, "--ask-password needs an interactive terminal"},
		{"both password flags", []string{"ssh", "lab", "--ask-password", "--password", "pw"}, "none of the others can be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := newRootCmd(&rootFlags{})
			root.SetArgs(tt.args)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}

	// Cobra warns on every use of a deprecated flag and hides it from help.
	pw := newSSHCmd().Flags().Lookup("password")
	if pw == nil || !strings.Contains(pw.Deprecated, "--ask-password") {
		t.Fatalf("--password must be deprecated in favour of --ask-password, got %+v", pw)
	}
}
