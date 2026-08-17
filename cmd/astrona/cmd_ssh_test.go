package main

import (
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
}
