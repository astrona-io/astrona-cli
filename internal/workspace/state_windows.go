//go:build windows

package workspace

import (
	"io/fs"
	"os"
)

// openNoFollow is not available on Windows; the regular-file check on the
// opened file still applies.
const openNoFollow = 0

// checkPrivate is a no-op on Windows: Unix mode bits and owners do not map
// to its ACLs, and the profile directory is already private to the user.
func checkPrivate(string, fs.FileInfo) error { return nil }

// processAlive reports whether pid names a live process. On Windows
// FindProcess opens a handle and fails when the process does not exist.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	p.Release()
	return true
}
