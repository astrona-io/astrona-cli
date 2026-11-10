//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup starts cmd in a process group of its own, so a Ctrl+C at
// the terminal reaches only astrona connect, which passes it on once
// (childRunner's Cancel). In connect's group the child would get the
// terminal's copy as well and take the second as "quit now".
func ownProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
