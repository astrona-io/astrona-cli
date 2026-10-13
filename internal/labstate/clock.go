package labstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// remoteClock is what Astrona was last told about a run's clock: paused or
// running, for which lab session. Every lab window and the usage reporter
// share it, so a pause or resume is queued once however many of them notice.
type remoteClock struct {
	Session string    `json:"session"`
	Paused  bool      `json:"paused"`
	At      time.Time `json:"at"`
}

// SetRemotePaused records that the site is to be told the run's clock paused
// (or resumed) at at, and reports whether that is news — false when the
// session's clock is already in that state. A new session (a renew) starts
// running.
func SetRemotePaused(lab, started, session string, paused bool, at time.Time) (bool, error) {
	dir, err := activityDir(lab, started)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "clock.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return false, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	path := filepath.Join(dir, "clock.json")
	var current remoteClock
	if data, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(data, &current)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if current.Session != session {
		current = remoteClock{Session: session}
	}
	if current.Paused == paused {
		return false, nil
	}
	data, err := json.Marshal(remoteClock{Session: session, Paused: paused, At: at.UTC()})
	if err != nil {
		return false, err
	}
	return true, writeFileAtomic(path, data)
}

// RemotePaused reports whether the site was last told the session's clock is
// paused.
func RemotePaused(lab, started, session string) (bool, error) {
	dir, err := activityDir(lab, started)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "clock.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var current remoteClock
	if err := json.Unmarshal(data, &current); err != nil {
		return false, err
	}
	return current.Session == session && current.Paused, nil
}

// LastPresence is the newest heartbeat any lab window published for the run,
// and whether one ever did (seen false: the run was never opened in a lab
// window, so nothing decides about pauses).
func LastPresence(lab, started string) (last time.Time, seen bool, err error) {
	if started == "" {
		return time.Time{}, false, nil
	}
	dir, err := activityDir(lab, started)
	if err != nil {
		return time.Time{}, false, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "presence"))
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "presence", entry.Name()))
		if err != nil {
			return time.Time{}, false, err
		}
		var presence activityPresence
		if err := json.Unmarshal(data, &presence); err != nil {
			return time.Time{}, false, err
		}
		seen = true
		if presence.Updated.After(last) {
			last = presence.Updated
		}
	}
	return last, seen, nil
}
