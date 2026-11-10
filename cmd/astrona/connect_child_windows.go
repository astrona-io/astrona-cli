//go:build windows

package main

import "os/exec"

// ownProcessGroup does nothing on Windows: astrona runs labs under WSL,
// where the unix build applies.
func ownProcessGroup(*exec.Cmd) {}
