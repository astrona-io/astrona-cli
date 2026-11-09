package labops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

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

// SignalTimeout bounds one attempt to reach the site, so a lab window or a
// destroy never hangs on a bad connection.
const SignalTimeout = 6 * time.Second

// SiteSender sends outbox signals with the signed-in account of each site.
type SiteSender struct {
	Client *account.Client
	Store  account.Store
	creds  map[string]*account.Credentials
}

// NewSiteSender is a SiteSender for the account AccountDeps names.
func NewSiteSender() (*SiteSender, error) {
	client, store, _, err := AccountDeps()
	if err != nil {
		return nil, err
	}
	return &SiteSender{Client: client, Store: store, creds: map[string]*account.Credentials{}}, nil
}

// Active is the signed-in account for site, looked up once per sender; an
// error is already classified for the outbox.
func (s *SiteSender) Active(ctx context.Context, site string) (*account.Credentials, error) {
	if cr := s.creds[site]; cr != nil {
		return cr, nil
	}
	cr, err := s.Client.Active(ctx, s.Store, site)
	if err != nil {
		return nil, s.Classify(err)
	}
	s.creds[site] = cr
	return cr, nil
}

// Classify turns an account error into what the outbox does with the signal.
func (s *SiteSender) Classify(err error) error {
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

func (s *SiteSender) Pause(ctx context.Context, site, session string, at time.Time) error {
	cr, err := s.Active(ctx, site)
	if err != nil {
		return err
	}
	return s.Classify(s.Client.PauseLabSession(ctx, s.Store, cr, session, false, at))
}

func (s *SiteSender) Resume(ctx context.Context, site, session string, at time.Time) error {
	cr, err := s.Active(ctx, site)
	if err != nil {
		return err
	}
	return s.Classify(s.Client.PauseLabSession(ctx, s.Store, cr, session, true, at))
}

func (s *SiteSender) Stop(ctx context.Context, site, session, reason string, at time.Time, usage *account.UsageReport) error {
	cr, err := s.Active(ctx, site)
	if err != nil {
		return err
	}
	return s.Classify(s.Client.StopLabSession(ctx, s.Store, cr, session, reason, usage, at))
}

func (s *SiteSender) Usage(ctx context.Context, site, session string, samples []account.UsageSample) error {
	cr, err := s.Active(ctx, site)
	if err != nil {
		return err
	}
	_, err = s.Client.SendLabUsage(ctx, s.Store, cr, session, samples)
	return s.Classify(err)
}

// FlushOutbox sends what is waiting, within timeout. Replaced in tests.
var FlushOutbox = func(ctx context.Context, timeout time.Duration) (outbox.Status, error) {
	sender, err := NewSiteSender()
	if err != nil {
		return outbox.Status{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return outbox.Flush(ctx, sender)
}

// QueueSignal writes ev for sess and tries to send it at once. The answer
// says whether it reached Astrona (outbox.Online) or waits on this machine.
//
// Another process may be flushing (the reporter, the flusher): then the signal
// is only "online" once this session has nothing left waiting.
func QueueSignal(ctx context.Context, sess *labstate.Session, ev outbox.Event) (outbox.Status, error) {
	if err := outbox.Add(sess.Site, sess.ID, ev); err != nil {
		return outbox.Status{}, err
	}
	deadline := time.Now().Add(SignalTimeout)
	for {
		status, err := FlushOutbox(ctx, time.Until(deadline))
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
	ClockLocal   = "local"   // no lab session (local only, signed out at run): nothing to tell
	ClockSame    = "same"    // the site already had it
	ClockSent    = "sent"    // Astrona has it
	ClockWaiting = "waiting" // kept here; sent when Astrona answers
)

// SignalClock tells Astrona the run's clock paused (or resumed) at at. Several
// lab windows and the reporter may notice the same change; it is queued once.
func SignalClock(ctx context.Context, lab, started string, paused bool, at time.Time) (string, outbox.Status, error) {
	st, err := labstate.Load(lab)
	if err != nil || st == nil || st.LocalOnly || st.Session == nil || st.Session.ID == "" || st.StartedAt != started {
		return ClockLocal, outbox.Status{}, err
	}
	if finished, err := labstate.ReadSubmission(lab, started); err != nil || finished != nil {
		return ClockLocal, outbox.Status{}, err
	}
	changed, err := labstate.SetRemotePaused(lab, started, st.Session.ID, paused, at)
	if err != nil {
		return ClockLocal, outbox.Status{}, err
	}
	if !changed {
		status, _ := outbox.ReadStatus()
		return ClockSame, status, nil
	}
	kind := outbox.Resume
	if paused {
		kind = outbox.Pause
	}
	status, err := QueueSignal(ctx, st.Session, outbox.Event{Kind: kind, At: at.UTC()})
	if err != nil || status.State != outbox.Online {
		return ClockWaiting, status, err
	}
	return ClockSent, status, nil
}

// StopSession queues a stop for sess (a destroy, the watchdog's time limit)
// and tries to send it. Offline it waits on this machine: say so, once, on w.
func StopSession(ctx context.Context, w io.Writer, sess *labstate.Session, reason string, usage *account.UsageReport, what string) {
	status, err := QueueSignal(ctx, sess, outbox.Event{Kind: outbox.Stop, At: time.Now().UTC(), Reason: reason, Usage: usage})
	switch {
	case err != nil:
		ui.Fwarnf(w, "couldn't save %s for Astrona: %s", what, err)
	case status.State == outbox.SignedOut:
		ui.Fwarnf(w, "%s is saved on this machine — sign in again (astrona login) to send it to %s", what, sess.Site)
	case status.State != outbox.Online:
		ui.Fwarnf(w, "%s couldn't reach %s — it is saved on this machine and sent when Astrona is reachable", what, sess.Site)
	}
}

// RunOutboxFlusher retries until nothing waits, Astrona needs a new sign-in,
// or for at most `budget`; one flusher at a time. It is the body of the
// hidden `astrona outbox-flush` helper.
func RunOutboxFlusher(ctx context.Context, budget time.Duration) error {
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
		status, err := FlushOutbox(ctx, 20*time.Second)
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

// StartOutboxFlusher starts a detached `<this executable> outbox-flush`
// when signals are waiting, so they go out even when nothing else runs. It
// never blocks the caller, inherits this process's environment and logs to
// flush.log in the outbox directory.
func StartOutboxFlusher() {
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
