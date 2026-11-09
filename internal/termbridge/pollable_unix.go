//go:build unix

package termbridge

import (
	"os"
	"syscall"
)

// pollable returns a non-blocking duplicate of the PTY master, registered
// with the runtime poller so reads honour deadlines and Close. creack/pty
// hands out a blocking descriptor, whose Read cannot be interrupted while
// any process still holds the terminal open. On success the original is
// closed; on error the caller still owns it.
func pollable(f *os.File) (*os.File, error) {
	// Hold ForkLock so no child started in between inherits the duplicate
	// before it is marked close-on-exec.
	syscall.ForkLock.RLock()
	fd, err := syscall.Dup(int(f.Fd()))
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, err
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	_ = f.Close()
	return os.NewFile(uintptr(fd), f.Name()), nil
}
