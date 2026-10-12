package labstate

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// LockMutation prevents another CLI from replacing a run while its identity is
// being checked and acted on. Release it before entering an interactive shell.
func LockMutation(lab string) (func(), error) {
	path, err := Path(lab)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(filepath.Dir(path), "locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, filepath.Base(path)+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("lab %s is being changed by another command; try again when it finishes: %w", lab, err)
	}
	return sync.OnceFunc(func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); _ = file.Close() }), nil
}
