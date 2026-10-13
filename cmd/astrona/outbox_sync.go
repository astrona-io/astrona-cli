package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/account"
	"astrona/internal/labstate"
	"astrona/internal/outbox"
	"astrona/internal/ui"
)

// Everything the CLI tells Astrona about a running lab goes through the
// outbox (internal/outbox): it is written to disk first, then sent. Offline,
// it waits there and goes out in order once the site answers again — from the
// lab window, the usage reporter, the next astrona command (which starts a
// detached `astrona outbox-flush`) or that flusher.

// signalTimeout bounds one attempt to reach the site, so a lab window or a
// destroy never hangs on a bad connection.
const signalTimeout = 6 * time.Second

// siteSender sends outbox signals with the signed-in account of each site.
type siteSender struct {
	client *account.Client
	store  account.Store
	creds  map[string]*account.Credentials
}

func newSiteSender() (*siteSender, error) {
	client, store, _, err := accountDeps()
	if err != nil {
		return nil, err
	}
	return &siteSender{client: client, store: store, creds: map[string]*account.Credentials{}}, nil
}

func (s *siteSender) active(ctx context.Context, site string) (*account.Credentials, error) {
	if cr := s.creds[site]; cr != nil {
		return cr, nil
	}
	cr, err := s.client.Active(ctx, s.store, site)
	if err != nil {
		return nil, s.classify(err)
	}
	s.creds[site] = cr
	return cr, nil
}

// classify turns an account error into what the outbox does with the signal.
func (s *siteSender) classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, account.ErrSignedOut):
		return fmt.Errorf("%w: %w", outbox.ErrSignedOut, err)
	case account.Permanent(err):
		return &outbox.PermanentError{Err: err}
	}
	return err
}

func (s *siteSender) Pause(ctx context.Context, site, session string, at time.Time) error {
	cr, err := s.active(ctx, site)
	if err != nil {
		return err
	}
	return s.classify(s.client.PauseLabSession(ctx, s.store, cr, session, false, at))
}

func (s *siteSender) Resume(ctx context.Context, site, session string, at time.Time) error {
	cr, err := s.active(ctx, site)
	if err != nil {
		return err
	}
	return s.classify(s.client.PauseLabSession(ctx, s.store, cr, session, true, at))
}

func (s *siteSender) Stop(ctx context.Context, site, session, reason string, at time.Time, usage *account.UsageReport) error {
	cr, err := s.active(ctx, site)
	if err != nil {
		return err
	}
	return s.classify(s.client.StopLabSession(ctx, s.store, cr, session, reason, usage, at))
}

func (s *siteSender) Usage(ctx context.Context, site, session string, samples []account.UsageSample) error {
	cr, err := s.active(ctx, site)
	if err != nil {
		return err
	}
	_, err = s.client.SendLabUsage(ctx, s.store, cr, session, samples)
	return s.classify(err)
}

// flushOutbox sends what is waiting, within timeout. Replaced in tests.
var flushOutbox = func(ctx context.Context, timeout time.Duration) (outbox.Status, error) {
	sender, err := newSiteSender()
	if err != nil {
		return outbox.Status{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return outbox.Flush(ctx, sender)
}

// queueSignal writes ev for sess and tries to send it at once. The answer
// says whether it reached Astrona (outbox.Online) or waits on this machine.
//
// Another process may be flushing (the reporter, the flusher): then the signal
// is only "online" once this session has nothing left waiting.
func queueSignal(ctx context.Context, sess *labstate.Session, ev outbox.Event) (outbox.Status, error) {
	if err := outbox.Add(sess.Site, sess.ID, ev); err != nil {
		return outbox.Status{}, err
	}
	deadline := time.Now().Add(signalTimeout)
	for {
		status, err := flushOutbox(ctx, time.Until(deadline))
		if err != nil || status.State != outbox.Online {
			return status, err
		}
		left, err := outbox.PendingFor(sess.ID)
		if err != nil || left == 0 {
			return status, err
		}
		if time.Now().Add(500 * time.Millisecond).After(deadline) {
			status.State, status.Pending = outbox.Offline, max(status.Pending, left)
			status.Error = "Astrona did not answer in time"
			return status, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// Clock sync outcomes for the lab window.
const (
	clockLocal   = "local"   // no lab session (local only, signed out at run): nothing to tell
	clockSame    = "same"    // the site already had it
	clockSent    = "sent"    // Astrona has it
	clockWaiting = "waiting" // kept here; sent when Astrona answers
)

// signalClock tells Astrona the run's clock paused (or resumed) at at. Several
// lab windows and the reporter may notice the same change; it is queued once.
func signalClock(ctx context.Context, lab, started string, paused bool, at time.Time) (string, outbox.Status, error) {
	st, err := labstate.Load(lab)
	if err != nil || st == nil || st.LocalOnly || st.Session == nil || st.Session.ID == "" || st.StartedAt != started {
		return clockLocal, outbox.Status{}, err
	}
	changed, err := labstate.SetRemotePaused(lab, started, st.Session.ID, paused, at)
	if err != nil {
		return clockLocal, outbox.Status{}, err
	}
	if !changed {
		status, _ := outbox.ReadStatus()
		return clockSame, status, nil
	}
	kind := outbox.Resume
	if paused {
		kind = outbox.Pause
	}
	status, err := queueSignal(ctx, st.Session, outbox.Event{Kind: kind, At: at.UTC()})
	if err != nil || status.State != outbox.Online {
		return clockWaiting, status, err
	}
	return clockSent, status, nil
}

// stopSession queues a stop for sess (a destroy, the watchdog's time limit)
// and tries to send it. Offline it waits on this machine: say so, once.
func stopSession(ctx context.Context, sess *labstate.Session, reason string, usage *account.UsageReport, what string) {
	status, err := queueSignal(ctx, sess, outbox.Event{Kind: outbox.Stop, At: time.Now().UTC(), Reason: reason, Usage: usage})
	switch {
	case err != nil:
		ui.Warnf("couldn't save %s for Astrona: %s", what, err)
	case status.State == outbox.SignedOut:
		ui.Warnf("%s is saved on this machine — sign in again (astrona login) to send it to %s", what, sess.Site)
	case status.State != outbox.Online:
		ui.Warnf("%s couldn't reach %s — it is saved on this machine and sent when Astrona is reachable", what, sess.Site)
	}
}

// startOutboxFlusher starts a detached `astrona outbox-flush` when signals are
// waiting, so they go out even when nothing else runs. It never blocks the
// command that starts it.
func startOutboxFlusher() {
	if n, err := outbox.Pending(); err != nil || n == 0 {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	dir, err := outbox.Dir()
	if err != nil {
		return
	}
	logF, err := os.OpenFile(filepath.Join(dir, "flush.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer logF.Close()
	cmd := exec.Command(exe, "outbox-flush")
	cmd.Stdout, cmd.Stderr = logF, logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}

func newOutboxFlushCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "outbox-flush",
		Short:  "Send lab signals kept while offline (started by astrona when some are waiting)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOutboxFlusher(cmd.Context(), 30*time.Minute)
		},
	}
}

// runOutboxFlusher retries until nothing waits, Astrona needs a new sign-in,
// or for at most `budget`; one flusher at a time.
func runOutboxFlusher(ctx context.Context, budget time.Duration) error {
	dir, err := outbox.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "flusher.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil // another flusher is at it
	}
	if ctx == nil {
		ctx = context.Background()
	}
	until := time.Now().Add(budget)
	wait := 15 * time.Second
	for time.Now().Before(until) {
		status, err := flushOutbox(ctx, 20*time.Second)
		if err == nil && (status.Pending == 0 || status.State == outbox.SignedOut) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		wait = min(2*wait, 2*time.Minute)
	}
	return nil
}
