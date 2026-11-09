//go:build unix

package filelock

import (
	"errors"
	"os"
	"syscall"
)

// openFlags refuses a lock path that is a symlink.
const openFlags = syscall.O_NOFOLLOW

func lockFile(f *os.File, wait bool) error {
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	for {
		err := syscall.Flock(int(f.Fd()), how)
		switch {
		case errors.Is(err, syscall.EINTR):
			continue
		case errors.Is(err, syscall.EWOULDBLOCK):
			return errBusy
		}
		return err
	}
}

func unlockFile(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
