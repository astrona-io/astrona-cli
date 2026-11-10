package labstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// WaitMutation waits for a lab mutation to finish or ctx to be cancelled.
// Detached cleanup must not abandon an expired run because renewal is busy.
func WaitMutation(ctx context.Context, lab string) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		release, err := LockMutation(lab)
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return release, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

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
