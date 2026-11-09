//go:build !windows

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// spawnServer starts `<this executable> ui serve` in its own session, so it
// outlives the command that started it and a Ctrl-C there does not reach
// it. Its output goes to server.log in StateDir.
func spawnServer() (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	dir, err := StateDir()
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	if err := privateDir(dir); err != nil {
		return 0, err
	}
	logF, err := os.OpenFile(filepath.Join(dir, "server.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND|openNoFollow, 0o600)
	if err != nil {
		return 0, err
	}
	defer logF.Close()
	cmd := exec.Command(exe, "ui", "serve")
	cmd.Stdout, cmd.Stderr = logF, logF
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Reap the child if it exits while this process still runs, so a quick
	// failure is not reported as a live zombie.
	go func() { _ = cmd.Wait() }()
	return pid, nil
}
