//go:build windows

package executor

import "os/exec"

// Windows has no process groups to signal; astrona runs labs under WSL,
// so only the direct child is killed here.
func setProcessGroup(*exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return errNoProcess
	}
	return cmd.Process.Kill()
}

func forwardSignals(*exec.Cmd) func() { return func() {} }
