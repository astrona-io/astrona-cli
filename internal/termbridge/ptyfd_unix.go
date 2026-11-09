//go:build unix

package termbridge

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// pollable returns a non-blocking duplicate of the PTY master, registered
// with the runtime poller so reads and writes honour deadlines and Close.
// creack/pty hands out a blocking descriptor, whose Read cannot be
// interrupted while any process still holds the terminal open. On success
// the original is closed; on error the caller still owns it.
//
// Never call Fd on the result: it switches the descriptor back to blocking
// mode, after which deadlines no longer interrupt a read or write.
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

// setsize sets the terminal size without calling f.Fd (pty.Setsize does,
// which would make the master blocking again).
func setsize(f *os.File, cols, rows uint16) error {
	conn, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ioctlErr error
	if err := conn.Control(func(fd uintptr) {
		ioctlErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: cols, Row: rows})
	}); err != nil {
		return err
	}
	return ioctlErr
}
