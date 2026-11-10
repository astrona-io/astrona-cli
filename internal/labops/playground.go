package labops

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"astrona/internal/account"
	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// A playground started from the catalog is timed: its clock starts with
// `astrona run` (the site records it as playground time) and stops at
// `astrona destroy`, or at its time limit (config.yaml metadata.timeLimit,
// else the site's default), when a detached watchdog removes the cluster.

// IsPlaygroundName reports whether a catalog name is a module's playground.
func IsPlaygroundName(name string) bool {
	return strings.HasSuffix(strings.TrimRight(name, "/"), "/playground")
}

// ClampDeadline keeps a playground deadline the site sent (RFC 3339) within
// [now+config.MinTimeLimit, now+limitMinutes] — the time limit the CLI
// asked for, or config.MaxTimeLimit when it asked for none — so a site
// answer can never have the watchdog remove the lab at once. An
// unparsable deadline is returned as is (the watchdog then isn't started).
func ClampDeadline(raw string, now time.Time, limitMinutes int) string {
	d, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	limit := config.MaxTimeLimit
	if limitMinutes > 0 {
		limit = time.Duration(limitMinutes) * time.Minute
	}
	earliest, latest := now.Add(config.MinTimeLimit), now.Add(limit)
	switch {
	case d.Before(earliest):
		d = earliest
	case d.After(latest):
		d = latest
	default:
		return raw
	}
	return d.UTC().Format(time.RFC3339)
}

// StartPlaygroundWatchdog starts the detached process that ends the
// playground at its deadline, remembers its pid for destroy, and says on w
// when that is (warnings on warn). Best effort: without it the clock still
// stops on the site at the deadline, but the cluster stays until `astrona
// destroy`.
func StartPlaygroundWatchdog(w, warn io.Writer, clusterName string, sess *labstate.Session) {
	deadline, err := time.Parse(time.RFC3339, sess.DeadlineAt)
	if err != nil {
		ui.Fwarnf(warn, "the site sent no time limit for this playground — it runs until `astrona destroy`")
		return
	}
	pid, err := SpawnWatchdog(clusterName)
	if err != nil {
		ui.Fwarnf(warn, "couldn't start the playground's timer (%s) — run `astrona destroy %s` when you are done", err, clusterName)
		return
	}
	if err := labstate.Update(clusterName, func(s *labstate.State) {
		if s.Session != nil {
			s.Session.WatchdogPID = pid
		}
	}); err != nil {
		ui.Fwarnf(warn, "could not remember the playground's timer (%s)", err)
	}
	fmt.Fprintf(w, "Playground ready. It stops and is removed at %s (in %s);\n`astrona destroy %s` ends it sooner.\n",
		deadline.Local().Format("15:04"), time.Until(deadline).Round(time.Minute), clusterName)
}

// SpawnWatchdog runs `astrona playground-watchdog <cluster>` in its own
// session (Setsid), so closing the terminal doesn't end it. Its output goes
// to a log next to the lab's state. Replaced in tests.
var SpawnWatchdog = func(clusterName string) (int, error) {
	return SpawnDetached("playground-watchdog", clusterName, ".watchdog.log")
}

// SpawnDetached runs `<this executable> <command> <cluster>` in its own
// session, logging to <cluster><logSuffix> in the lab state directory. It
// inherits this process's environment.
func SpawnDetached(command, clusterName, logSuffix string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	dir, err := labstate.Dir()
	if err != nil {
		return 0, err
	}
	logF, err := os.OpenFile(filepath.Join(dir, clusterName+logSuffix), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer logF.Close()
	cmd := exec.Command(exe, command, clusterName)
	cmd.Stdout, cmd.Stderr = logF, logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

// PlaygroundDeadline identifies the run and deadline a watchdog may expire.
type PlaygroundDeadline struct {
	RunID      string
	SessionID  string
	DeadlineAt string
}

func (d PlaygroundDeadline) matches(st *labstate.State) bool {
	return st != nil && !st.LocalOnly && st.Session.IsPlayground() &&
		RunID(st) == d.RunID && st.Session.ID == d.SessionID && st.Session.DeadlineAt == d.DeadlineAt
}

// WatchPlayground waits until the playground's deadline, checking every
// tick that it still runs under the same session (destroyed, replaced by
// a new run, or renewed — which starts its own watchdog: nothing to do).
// At the deadline expire takes the maintenance lock and calls ExpirePlayground.
// A cancelled ctx ends it quietly — that is how
// `astrona destroy` stops it. Progress lines go to w, warnings to warn.
// It is the body of the hidden `astrona playground-watchdog` helper.
func WatchPlayground(ctx context.Context, w, warn io.Writer, clusterName string, tick time.Duration, expire func(context.Context, string, PlaygroundDeadline) error) error {
	st, err := labstate.Load(clusterName)
	if err != nil || st == nil || st.LocalOnly || !st.Session.IsPlayground() {
		return err
	}
	sess := *st.Session
	expected := PlaygroundDeadline{RunID: RunID(st), SessionID: sess.ID, DeadlineAt: sess.DeadlineAt}
	deadline, err := time.Parse(time.RFC3339, sess.DeadlineAt)
	if err != nil {
		return fmt.Errorf("playground %s has no deadline: %w", clusterName, err)
	}
	fmt.Fprintf(w, "%s watching %s until %s\n", time.Now().Format(time.RFC3339), clusterName, deadline.Format(time.RFC3339))
	for {
		// Wall-clock compare each tick, so a laptop that slept still stops on time.
		wait := time.Until(deadline)
		if wait <= 0 {
			break
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(min(wait, tick)):
		}
		cur, err := labstate.Load(clusterName)
		if err == nil && !expected.matches(cur) {
			return nil
		}
	}
	current, err := labstate.Load(clusterName)
	if err != nil {
		return err
	}
	if ctx.Err() != nil || !expected.matches(current) {
		return nil
	}
	fmt.Fprintf(w, "%s time limit reached: stopping %s\n", time.Now().Format(time.RFC3339), clusterName)
	err = expire(ctx, clusterName, expected)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// ExpirePlayground checks the watchdog's identity under the mutation lock before
// stopping its session and deleting the lab. The caller holds the maintenance
// lock; renewal and replacement use the same mutation lock.
func ExpirePlayground(ctx context.Context, clusterName string, expected PlaygroundDeadline, rep *ui.Reporter, opts DestroyOptions) error {
	if ctx.Err() != nil {
		return nil
	}
	release, err := labstate.WaitMutation(ctx, clusterName)
	if err != nil {
		return err
	}
	defer release()
	current, err := labstate.Load(clusterName)
	if err != nil {
		return err
	}
	if ctx.Err() != nil || !expected.matches(current) {
		return nil
	}
	deadline, err := time.Parse(time.RFC3339, expected.DeadlineAt)
	if err != nil || time.Now().Before(deadline) {
		return fmt.Errorf("playground %s has not reached a valid deadline", clusterName)
	}
	StopPlaygroundClock(ctx, opts.ForgetWarn, current.Session, "time_limit", RunUsageReport(clusterName, current))
	if ctx.Err() != nil {
		return nil
	}
	return DestroyByNameLocked(clusterName, rep, opts)
}

// StopPlaygroundClock tells the site the playground ended, through the
// outbox: offline, the stop is kept and sent later with the time it
// happened. The site keeps the first stop, so the watchdog's "time_limit"
// stands even if destroy reports again. usage (may be nil) is what the
// playground used. Notices and warnings go to warn.
func StopPlaygroundClock(ctx context.Context, warn io.Writer, sess *labstate.Session, reason string, usage *account.UsageReport) {
	NoticeOtherSite(warn, sess.Site, "the playground time")
	StopSession(ctx, warn, sess, reason, usage, "the playground's stop")
}

// EndRunSession runs when a lab is forgotten (destroyed, or replaced by a
// fresh run): a playground's watchdog is stopped — unless that is us — and
// its clock is stopped as "destroyed"; a lab's session is closed as
// "destroyed" (the site leaves one that already passed as it is).
// Warnings go to warn.
func EndRunSession(warn io.Writer, clusterName string) {
	st, err := labstate.Load(clusterName)
	if err != nil || st == nil || st.Session == nil {
		return
	}
	if !st.Session.IsPlayground() {
		if !st.LocalOnly {
			EndLabSession(context.Background(), warn, st.Session)
		}
		return
	}
	StopWatchdog(st.Session.WatchdogPID, clusterName)
	if !st.LocalOnly {
		StopPlaygroundClock(context.Background(), warn, st.Session, "destroyed", RunUsageReport(clusterName, st))
	}
}

// EndLabSession tells the site a lab was destroyed, so the attempt stops
// showing as running; it never counts unless it passed before. Offline, the
// closure is kept for later delivery, without usage or grading results.
func EndLabSession(ctx context.Context, warn io.Writer, sess *labstate.Session) {
	StopSession(ctx, warn, sess, "destroyed", nil, "the lab's end")
}

// EndOldSession runs before a lab is built again under the same name (run's
// start over, a full reset, or a run over what a vanished lab left behind):
// an old playground's watchdog — which would otherwise remove the new lab
// at the old deadline — and its clock are stopped, and the old session is
// forgotten. The rest (where the lab came from, the kubectl context to go
// back to) stays for the new run.
func EndOldSession(warn io.Writer, clusterName string) {
	EndRunSession(warn, clusterName)
	if err := labstate.Update(clusterName, func(s *labstate.State) { s.Session = nil }); err != nil {
		ui.Fwarnf(warn, "could not forget the lab's previous session: %s", err)
	}
}

// WatchdogCommandLine is pid's full command line (replaced in tests). The
// ps flags are the same on macOS and Linux; -ww stops it truncating.
var WatchdogCommandLine = func(pid int) (string, error) {
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", fmt.Errorf("failed to read command line of pid %d: %w", pid, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// IsWatchdogFor reports whether cmdline is `astrona playground-watchdog
// <clusterName>` — the process SpawnWatchdog started, not one that reused
// its pid.
func IsWatchdogFor(cmdline, clusterName string) bool {
	return isHelperFor(cmdline, "playground-watchdog", clusterName)
}

// isHelperFor reports whether cmdline is `astrona <command> <clusterName>`.
func isHelperFor(cmdline, command, clusterName string) bool {
	fields := strings.Fields(cmdline)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == command && fields[i+1] == clusterName {
			return true
		}
	}
	return false
}

// StopWatchdog ends clusterName's playground watchdog — unless that is us.
func StopWatchdog(pid int, clusterName string) {
	stopHelper(pid, "playground-watchdog", clusterName)
}

// stopHelper ends the detached `astrona <command> <clusterName>` at pid —
// unless that is us. The pid is only signalled while it still leads its own
// session (Setsid) and runs that command line: anything else reused the
// number and is left alone. It reports whether it signalled the helper.
func stopHelper(pid int, command, clusterName string) bool {
	if pid <= 0 || pid == os.Getpid() || !hypervisor.ProcessAlive(pid) {
		return false
	}
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		return false
	}
	if cmdline, err := WatchdogCommandLine(pid); err != nil || !isHelperFor(cmdline, command, clusterName) {
		return false
	}
	return syscall.Kill(-pid, syscall.SIGTERM) == nil
}
