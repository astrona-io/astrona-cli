package labops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"astrona/internal/account"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// ExtendMinutes is what one extension adds on the site ("Need more time"),
// as often as the student asks. Hands-on exams are never extended.
const ExtendMinutes = 30

// ErrNotExtendable: the run has nothing the site could add time to — an
// exam, a run without an Astrona lab session (local-only, signed out, a
// lab from your own files), or a playground already past its time limit.
var ErrNotExtendable = errors.New("this run can't get more time")

// ExtendResult is what an extended run got.
type ExtendResult struct {
	Cluster    string
	Playground bool
	// Deadline is when a playground now stops and is removed. Zero for a
	// lab: its extra time goes on its expected time on the site, and no
	// local deadline is kept for it.
	Deadline time.Time
	// ExtraMinutes is all the time added to the run so far, ExtendCount
	// how many times.
	ExtraMinutes int
	ExtendCount  int
}

// ExtendableSession reports why st's run can't be extended (nil when it
// can be asked): only a lab or playground with an Astrona lab session, on
// a run that isn't an exam. It reads nothing but st and the exam clock.
func ExtendableSession(clusterName string, st *labstate.State) error {
	switch {
	case IsExamCluster(clusterName):
		return fmt.Errorf("%w: a hands-on exam's time is never extended", ErrNotExtendable)
	case st == nil:
		return fmt.Errorf("%w: astrona remembers no run of it", ErrNotExtendable)
	case st.LocalOnly:
		return fmt.Errorf("%w: it runs --local-only, so Astrona keeps no clock for it", ErrNotExtendable)
	case st.Session == nil || st.Session.ID == "" || st.Session.Site == "":
		return fmt.Errorf("%w: it has no Astrona lab session (signed out, or a lab from your own files) — its time isn't limited by Astrona", ErrNotExtendable)
	}
	if clock, err := exam.Load(clusterName); err == nil && clock != nil {
		return fmt.Errorf("%w: an exam's time limit is never extended", ErrNotExtendable)
	}
	return nil
}

// ExtendRun asks the site for ExtendMinutes more on clusterName's run, under
// the lab's mutation lock. check, when not nil, is run on the saved state
// under the lock first (the browser workspace's run-identity check); its
// error is returned as is. A playground's new deadline is saved and its
// watchdog replaced with one for it; a lab keeps its saved session's expiry
// and time limit in step. Notices and warnings go to warn.
func ExtendRun(ctx context.Context, warn io.Writer, clusterName string, check func(*labstate.State) error) (*ExtendResult, error) {
	release, err := labstate.LockMutation(clusterName)
	if err != nil {
		return nil, err
	}
	defer release()
	st, err := labstate.Load(clusterName)
	if err != nil {
		return nil, err
	}
	if check != nil {
		if err := check(st); err != nil {
			return nil, err
		}
	}
	if err := ExtendableSession(clusterName, st); err != nil {
		return nil, err
	}
	sess := *st.Session
	if sess.IsPlayground() {
		if deadline, err := time.Parse(time.RFC3339, sess.DeadlineAt); err == nil && !time.Now().Before(deadline) {
			return nil, fmt.Errorf("%w: the playground's time limit already passed — run it again", ErrNotExtendable)
		}
	}
	client, store, _, err := AccountDeps()
	if err != nil {
		return nil, err
	}
	creds, err := client.Active(ctx, store, sess.Site)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	NoticeOtherSite(warn, creds.Site, "the request for more time")
	ext, err := client.ExtendLabSession(ctx, store, creds, sess.ID)
	if err != nil {
		return nil, err
	}
	res := &ExtendResult{Cluster: clusterName, Playground: sess.IsPlayground(), ExtraMinutes: ext.ExtraMinutes, ExtendCount: ext.ExtendCount}
	if res.Playground {
		// Never a deadline outside what the site itself says the run may
		// take (its time limit plus every extension), nor one that would
		// have the watchdog remove the lab at once.
		limit := ext.MaxMinutes
		if limit <= 0 {
			limit = int(config.MaxTimeLimit / time.Minute)
		}
		ext.DeadlineAt = ClampDeadline(ext.DeadlineAt, time.Now(), limit+ext.ExtraMinutes)
		deadline, err := time.Parse(time.RFC3339, ext.DeadlineAt)
		if err != nil {
			return nil, fmt.Errorf("the site added the time but sent no new deadline: %w", err)
		}
		res.Deadline = deadline
	}
	if err := labstate.Update(clusterName, func(s *labstate.State) {
		if s.Session == nil || s.Session.ID != sess.ID {
			return
		}
		if ext.ExpiresAt != "" {
			s.Session.ExpiresAt = ext.ExpiresAt
		}
		if ext.MaxMinutes > 0 {
			s.Session.MaxMinutes = ext.MaxMinutes
		}
		if res.Playground {
			s.Session.DeadlineAt = ext.DeadlineAt
		}
	}); err != nil {
		return nil, fmt.Errorf("the site added the time, but the new deadline couldn't be saved: %w", err)
	}
	if res.Playground {
		// The old watchdog would stop on its own (its deadline no longer
		// matches); replace it now with one for the new deadline.
		restartWatchdog(warn, clusterName, sess.WatchdogPID)
	}
	return res, nil
}

// restartWatchdog replaces a playground's watchdog (pid, if it still runs)
// with one for the deadline saved now, and remembers the new pid.
func restartWatchdog(warn io.Writer, clusterName string, pid int) {
	StopWatchdog(pid, clusterName)
	newPID, err := SpawnWatchdog(clusterName)
	if err != nil {
		ui.Fwarnf(warn, "couldn't restart the playground's timer (%s) — run `astrona destroy %s` when you are done", err, clusterName)
		return
	}
	_ = labstate.Update(clusterName, func(s *labstate.State) {
		if s.Session != nil {
			s.Session.WatchdogPID = newPID
		}
	})
}

// RunningLabs lists the remembered labs that still exist: all of them, or
// the one name matches — its cluster name (with or without the prefix) or
// its catalog name (with or without the owner).
func RunningLabs(name string) ([]string, error) {
	names, err := labstate.Names()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range names {
		st, err := labstate.Load(c)
		if err != nil || st == nil {
			continue
		}
		if name != "" && c != config.NormalizeClusterName(name) && (st.Session == nil || !SameCatalogName(st.Session.Lab, name)) {
			continue
		}
		if !PlaygroundExists(c) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// IsExtendRefused reports whether err is the site refusing the extension
// with code.
func IsExtendRefused(err error, code string) bool {
	var r *account.ExtendRefusedError
	return errors.As(err, &r) && r.Code == code
}
