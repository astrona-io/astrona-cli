//go:build !windows

package executor

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup sends SIGKILL to cmd's whole process group (its pgid is
// its pid: setProcessGroup).
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errNoProcess
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// forwardSignals keeps Ctrl+C working while cmd runs. Its own process
// group is not the terminal's foreground group, so the terminal's SIGINT
// reaches only astrona: pass it (and SIGTERM/SIGHUP) on to cmd's group,
// as the terminal would have, then raise it again here so astrona reacts
// as it would without this handler — unless astrona handles interrupts
// itself (HandleInterrupts): it got the same signal already, and a raised
// copy would read as a second Ctrl+C. The returned func stops forwarding.
func forwardSignals(cmd *exec.Cmd) func() {
	ch := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		select {
		case sig := <-ch:
			s := sig.(syscall.Signal)
			_ = syscall.Kill(-cmd.Process.Pid, s)
			signal.Stop(ch)
			if !interruptsHandled.Load() {
				_ = syscall.Kill(os.Getpid(), s)
			}
		case <-done:
		}
	}()
	return func() {
		close(done)
		signal.Stop(ch)
	}
}
