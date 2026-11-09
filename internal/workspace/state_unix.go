//go:build !windows

package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// openNoFollow refuses to follow a symlink in the last path element.
const openNoFollow = syscall.O_NOFOLLOW

// checkPrivate refuses a file or directory that another user owns or that
// group or others can access.
func checkPrivate(path string, fi fs.FileInfo) error {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is owned by another user (uid %d)", path, st.Uid)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is accessible by other users (mode %v); remove it and restart the workspace", path, fi.Mode().Perm())
	}
	return nil
}

// processAlive reports whether pid names a live process (signal 0 probe;
// EPERM means it exists but belongs to someone else).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
