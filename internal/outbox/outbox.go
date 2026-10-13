// Package outbox keeps what the CLI tells Astrona about a running lab — clock
// pauses and resumes, stops, resource samples — on disk until the site has it.
// Every signal carries the time it happened, so signals kept while offline are
// sent later, in order, and land where they belong. One file per lab session
// under ~/.astrona/outbox; a lock serializes writers and flushers across
// processes (the lab window, the usage reporter, destroy, the flusher).
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"astrona/internal/account"
	"astrona/internal/apphome"
)

// Kinds of signal.
const (
	Pause  = "pause"
	Resume = "resume"
	Stop   = "stop"
	Usage  = "usage"
)

// MaxUsageSamples bounds what one session keeps while offline: a day of
// samples every 15 seconds, with room to spare. Older samples go first.
const MaxUsageSamples = 6000

// Event is one signal for a lab session.
type Event struct {
	Kind   string               `json:"kind"`
	At     time.Time            `json:"at"`
	Reason string               `json:"reason,omitempty"`
	Usage  *account.UsageReport `json:"usage,omitempty"`
	Sample *account.UsageSample `json:"sample,omitempty"`
}

// queue is one session's file.
type queue struct {
	Site    string  `json:"site"`
	Session string  `json:"session"`
	Events  []Event `json:"events"`
}

// Connection states in the status file.
const (
	Online    = "online"
	Offline   = "offline"
	SignedOut = "signed_out"
)

// Status is how the last flush went, for the lab window's connection marker.
type Status struct {
	State   string    `json:"state"`
	Checked time.Time `json:"checked"`
	Pending int       `json:"pending"`
	Error   string    `json:"error,omitempty"`
}

// Sender delivers signals to the site. PermanentError drops the signal;
// ErrSignedOut keeps it until `astrona login`. Other errors keep it for retry.
type Sender interface {
	Pause(ctx context.Context, site, session string, at time.Time) error
	Resume(ctx context.Context, site, session string, at time.Time) error
	Stop(ctx context.Context, site, session, reason string, at time.Time, usage *account.UsageReport) error
	Usage(ctx context.Context, site, session string, samples []account.UsageSample) error
}

// PermanentError marks an error the site gave for good (a session that is gone, a
// request it refuses): the signal is dropped instead of retried.
type PermanentError struct{ Err error }

func (p *PermanentError) Error() string { return p.Err.Error() }
func (p *PermanentError) Unwrap() error { return p.Err }

// ErrSignedOut: no usable sign-in for the site; signals wait for `astrona login`.
var ErrSignedOut = errors.New("signed out")

// Dir is ~/.astrona/outbox.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, apphome.Name(), "outbox"), nil
}

func validSession(session string) bool {
	return session != "" && len(session) <= 64 && !strings.ContainsAny(session, `/\.`+"\x00")
}

// lock takes the outbox lock; with wait false it gives up at once when another
// process holds it (ok false).
func lock(wait bool) (release func(), ok bool, err error) {
	dir, err := Dir()
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "outbox.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, true, nil
}

func queuePath(dir, session string) string { return filepath.Join(dir, session+".json") }

func readQueue(path string) (*queue, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var q queue
	if err := json.Unmarshal(data, &q); err != nil {
		return nil, fmt.Errorf("outbox file %s: %w", filepath.Base(path), err)
	}
	return &q, nil
}

func writeQueue(path string, q *queue) error {
	if len(q.Events) == 0 {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := json.Marshal(q)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".outbox-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Add queues an event for site's session.
func Add(site, session string, ev Event) error {
	if !validSession(session) {
		return fmt.Errorf("invalid lab session id %q", session)
	}
	release, _, err := lock(true)
	if err != nil {
		return err
	}
	defer release()
	dir, _ := Dir()
	path := queuePath(dir, session)
	q, err := readQueue(path)
	if err != nil {
		return err
	}
	if q == nil {
		q = &queue{Site: site, Session: session}
	}
	q.Events = append(q.Events, ev)
	if ev.Kind == Usage {
		trimUsage(q)
	}
	return writeQueue(path, q)
}

// trimUsage drops the oldest samples beyond MaxUsageSamples; other signals stay.
func trimUsage(q *queue) {
	count := 0
	for _, ev := range q.Events {
		if ev.Kind == Usage {
			count++
		}
	}
	drop := count - MaxUsageSamples
	if drop <= 0 {
		return
	}
	kept := q.Events[:0]
	for _, ev := range q.Events {
		if ev.Kind == Usage && drop > 0 {
			drop--
			continue
		}
		kept = append(kept, ev)
	}
	q.Events = kept
}

func sessionFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		name := e.Name()
		if e.Type().IsRegular() && strings.HasSuffix(name, ".json") && name != "status.json" && !strings.HasPrefix(name, ".") {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

// Pending counts the signals waiting to be sent.
func Pending() (int, error) {
	dir, err := Dir()
	if err != nil {
		return 0, err
	}
	paths, err := sessionFiles(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, path := range paths {
		q, err := readQueue(path)
		if err != nil {
			return 0, err
		}
		if q != nil {
			n += len(q.Events)
		}
	}
	return n, nil
}

// PendingFor counts the signals still waiting for one session.
func PendingFor(session string) (int, error) {
	if !validSession(session) {
		return 0, nil
	}
	dir, err := Dir()
	if err != nil {
		return 0, err
	}
	q, err := readQueue(queuePath(dir, session))
	if err != nil || q == nil {
		return 0, err
	}
	return len(q.Events), nil
}

// Flush sends what is waiting, oldest first per session, and records how it
// went (ReadStatus). It stops at the first signal the site could not be reached
// for, keeping it and everything after it. When another process is flushing,
// it returns that process's last status without waiting. Consecutive samples go
// in one post.
func Flush(ctx context.Context, s Sender) (Status, error) {
	release, ok, err := lock(false)
	if err != nil {
		return Status{}, err
	}
	if !ok {
		return ReadStatus()
	}
	defer release()
	dir, _ := Dir()
	paths, err := sessionFiles(dir)
	if err != nil {
		return Status{}, err
	}
	status := Status{State: Online, Checked: time.Now().UTC()}
	sent := false
	for _, path := range paths {
		q, err := readQueue(path)
		if err != nil || q == nil {
			continue
		}
		stop := false
		for len(q.Events) > 0 && !stop {
			n, err := sendNext(ctx, s, q)
			var permanent *PermanentError
			switch {
			case err == nil:
				sent = true
				q.Events = q.Events[n:]
			case errors.As(err, &permanent):
				q.Events = q.Events[n:]
			case errors.Is(err, ErrSignedOut):
				status.State, status.Error, stop = SignedOut, err.Error(), true
			default:
				status.State, status.Error, stop = Offline, err.Error(), true
			}
		}
		if err := writeQueue(path, q); err != nil {
			return status, err
		}
		status.Pending += len(q.Events)
		if status.State != Online {
			// Nothing else gets through either; count the rest and stop.
			for _, rest := range paths {
				if rest > path {
					if r, _ := readQueue(rest); r != nil {
						status.Pending += len(r.Events)
					}
				}
			}
			break
		}
	}
	// With nothing sent and nothing waiting, the connection was not tested:
	// keep the last known state rather than claiming to be online.
	if !sent && status.State == Online && status.Pending == 0 && len(paths) == 0 {
		if last, err := ReadStatus(); err == nil && last.State != "" {
			last.Pending = 0
			return last, nil
		}
	}
	return status, writeStatus(dir, status)
}

// sendNext sends the first signal (with the samples right after it, for a usage
// one) and says how many events that covered.
func sendNext(ctx context.Context, s Sender, q *queue) (int, error) {
	ev := q.Events[0]
	switch ev.Kind {
	case Pause:
		return 1, s.Pause(ctx, q.Site, q.Session, ev.At)
	case Resume:
		return 1, s.Resume(ctx, q.Site, q.Session, ev.At)
	case Stop:
		return 1, s.Stop(ctx, q.Site, q.Session, ev.Reason, ev.At, ev.Usage)
	case Usage:
		var samples []account.UsageSample
		n := 0
		for _, e := range q.Events {
			if e.Kind != Usage || len(samples) == account.MaxUsageSamplesPerPost {
				break
			}
			n++
			if e.Sample != nil {
				samples = append(samples, *e.Sample)
			}
		}
		if len(samples) == 0 {
			return n, nil
		}
		return n, s.Usage(ctx, q.Site, q.Session, samples)
	default:
		return 1, nil // from a newer astrona: nothing to do with it here
	}
}

func writeStatus(dir string, status Status) error {
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".status-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "status.json"))
}

// ReadStatus is how the last flush went; the zero Status when there was none.
func ReadStatus() (Status, error) {
	dir, err := Dir()
	if err != nil {
		return Status{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if errors.Is(err, os.ErrNotExist) {
		return Status{}, nil
	}
	if err != nil {
		return Status{}, err
	}
	var status Status
	err = json.Unmarshal(data, &status)
	return status, err
}
