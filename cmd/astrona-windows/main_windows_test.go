//go:build windows

package main

import (
	"strings"
	"syscall"
	"testing"
)

// With --exec, wsl.exe reads its argv the standard Windows way
// (CommandLineToArgvW rules). Check that the command line Go builds for
// forwardArgs (os/exec escapes each argument with syscall.EscapeArg) splits
// back into exactly the same arguments, so nothing a student types is
// merged, split or dropped on the way into WSL.
func TestForwardArgsSurviveWindowsCommandLine(t *testing.T) {
	args := append([]string{"wsl.exe"}, forwardArgs("Ubuntu", []string{
		"run", "a; rm -rf ~", "$(whoami)", `say "hi"`, `C:\path with\ trailing\`, "", "it's",
	})...)
	escaped := make([]string, len(args))
	for i, a := range args {
		escaped[i] = syscall.EscapeArg(a)
	}
	line, err := syscall.UTF16PtrFromString(strings.Join(escaped, " "))
	if err != nil {
		t.Fatal(err)
	}
	var argc int32
	argv, err := syscall.CommandLineToArgv(line, &argc)
	if err != nil {
		t.Fatal(err)
	}
	if int(argc) != len(args) {
		t.Fatalf("argc = %d, want %d", argc, len(args))
	}
	for i := range args {
		if got := syscall.UTF16ToString((*argv[i])[:]); got != args[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got, args[i])
		}
	}
}
