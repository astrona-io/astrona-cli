// Package filelock takes exclusive, advisory locks on a lock file across
// processes: flock on Unix, LockFileEx on Windows. A lock is released by its
// release func or when the holding process exits, so a crashed holder never
// leaves it stuck. The lock file itself stays in place.
package filelock

import (
	"context"
	"errors"
	"os"
	"time"
)

// errBusy is what lockFile returns when another process holds the lock.
var errBusy = errors.New("lock is held by another process")

// pollInterval is how often Wait retries a lock that is held.
const pollInterval = 25 * time.Millisecond

// Lock takes the exclusive lock on path, creating it 0600 if needed. With
// wait false it gives up at once when another process holds it (ok false,
// err nil); with wait true it blocks until the lock is free.
func Lock(path string, wait bool) (release func(), ok bool, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|openFlags, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := lockFile(f, wait); err != nil {
		f.Close()
		if errors.Is(err, errBusy) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() { unlockFile(f); f.Close() }, true, nil
}

// Wait takes the exclusive lock on path, retrying while another process
// holds it, until ctx is done.
func Wait(ctx context.Context, path string) (release func(), err error) {
	for {
		release, ok, err := Lock(path, false)
		if err != nil {
			return nil, err
		}
		if ok {
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
