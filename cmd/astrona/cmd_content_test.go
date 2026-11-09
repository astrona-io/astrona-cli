package main

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestNewContentCmd(t *testing.T) {
	cmd := newContentCmd()

	if cmd.Use != "content" {
		t.Errorf("expected command Use 'content', got %q", cmd.Use)
	}

	subcmds := cmd.Commands()
	if len(subcmds) != 3 {
		t.Fatalf("expected 3 subcommands, got %d", len(subcmds))
	}

	initCmd := cmd.Commands()[0]
	for _, c := range subcmds {
		if strings.HasPrefix(c.Use, "init ") {
			initCmd = c
		}
	}
	if initCmd.Use != "init <type> [path]" {
		t.Errorf("expected subcommand Use 'init <type> [path]', got %q", initCmd.Use)
	}

	var validateCmd *cobra.Command
	for _, c := range subcmds {
		if strings.HasPrefix(c.Use, "validate ") {
			validateCmd = c
		}
	}
	if validateCmd == nil {
		t.Fatal("expected a 'validate' subcommand to be registered")
	}
	if validateCmd.Use != "validate <type> <source>" {
		t.Errorf("expected subcommand Use 'validate <type> <source>', got %q", validateCmd.Use)
	}
	if flg := validateCmd.Flag("git-ref"); flg == nil {
		t.Error("expected flag \"git-ref\" to be defined")
	} else if flg.Value.Type() != "string" {
		t.Errorf("expected flag \"git-ref\" to be type string, got %s", flg.Value.Type())
	}

	var buildCmd *cobra.Command
	for _, c := range subcmds {
		if strings.HasPrefix(c.Use, "build ") {
			buildCmd = c
		}
	}
	if buildCmd == nil {
		t.Fatal("expected a 'build' subcommand to be registered")
	}
	if buildCmd.Use != "build <type> <source> [flags]" {
		t.Errorf("expected subcommand Use 'build <type> <source> [flags]', got %q", buildCmd.Use)
	}
	for _, ef := range []struct {
		name string
		typ  string
	}{
		{"git-ref", "string"},
		{"output", "string"},
		{"clean", "bool"},
	} {
		flg := buildCmd.Flag(ef.name)
		if flg == nil {
			t.Errorf("expected flag %q to be defined", ef.name)
		} else if flg.Value.Type() != ef.typ {
			t.Errorf("expected flag %q to be type %s, got %s", ef.name, ef.typ, flg.Value.Type())
		}
	}

	expectedFlags := []struct {
		name string
		typ  string
	}{
		{"path-id", "string"},
		{"slug", "string"},
		{"title", "string"},
		{"description", "string"},
		{"version", "string"},
		{"language", "string"},
		{"author-name", "string"},
		{"license", "string"},
		{"repo", "string"},
		{"create-repo", "bool"},
		{"github-org", "string"},
		{"github-repo", "string"},
		{"github-public", "bool"},
		{"codeowners", "string"},
	}

	for _, ef := range expectedFlags {
		flg := initCmd.Flag(ef.name)
		if flg == nil {
			t.Errorf("expected flag %q to be defined", ef.name)
		} else if flg.Value.Type() != ef.typ {
			t.Errorf("expected flag %q to be type %s, got %s", ef.name, ef.typ, flg.Value.Type())
		}
	}
}

func TestContentInitCmdInvalidType(t *testing.T) {
	initCmd := newContentInitCmd()
	initCmd.SetArgs([]string{"invalid", "some-path"})

	if err := initCmd.Execute(); err == nil {
		t.Error("expected error when running init with invalid type, but got nil")
	} else if !strings.Contains(err.Error(), "invalid content type") {
		t.Errorf("expected error message to contain 'invalid content type', but got: %v", err)
	}
}
