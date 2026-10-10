package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// A playground started from the catalog is timed: its clock starts with
// `astrona run` (the site records it as playground time) and stops at
// `astrona destroy`, or at its time limit (config.yaml metadata.timeLimit,
// else the site's default), when a detached watchdog removes the cluster.

// isPlaygroundName reports whether a catalog name is a module's playground.
func isPlaygroundName(name string) bool {
	return strings.HasSuffix(strings.TrimRight(name, "/"), "/playground")
}

// clampDeadline keeps a playground deadline the site sent (RFC 3339) within
// [now+config.MinTimeLimit, now+limitMinutes] — the time limit the CLI
// asked for, or config.MaxTimeLimit when it asked for none — so a site
// answer can never have the watchdog remove the lab at once. An
// unparsable deadline is returned as is (the watchdog then isn't started).
func clampDeadline(raw string, now time.Time, limitMinutes int) string {
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

// startPlaygroundWatchdog starts the detached process that ends the
// playground at its deadline, remembers its pid for destroy, and says when
// that is. Best effort: without it the clock still stops on the site at the
// deadline, but the cluster stays until `astrona destroy`.
func startPlaygroundWatchdog(clusterName string, sess *labstate.Session) {
	deadline, err := time.Parse(time.RFC3339, sess.DeadlineAt)
	if err != nil {
		ui.Warnf("the site sent no time limit for this playground — it runs until `astrona destroy`")
		return
	}
	pid, err := spawnWatchdog(clusterName)
	if err != nil {
		ui.Warnf("couldn't start the playground's timer (%s) — run `astrona destroy %s` when you are done", err, clusterName)
		return
	}
	if err := labstate.Update(clusterName, func(s *labstate.State) {
		if s.Session != nil {
			s.Session.WatchdogPID = pid
		}
	}); err != nil {
		ui.Warnf("could not remember the playground's timer (%s)", err)
	}
	fmt.Printf("Playground ready. It stops and is removed at %s (in %s);\n`astrona destroy %s` ends it sooner.\n",
		deadline.Local().Format("15:04"), time.Until(deadline).Round(time.Minute), clusterName)
}

// spawnWatchdog runs `astrona playground-watchdog <cluster>` in its own
// session (Setsid), so closing the terminal doesn't end it. Its output goes
// to a log next to the lab's state. Replaced in tests.
var spawnWatchdog = func(clusterName string) (int, error) {
	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	dir, err := labstate.Dir()
	if err != nil {
		return 0, err
	}
	logF, err := os.OpenFile(filepath.Join(dir, clusterName+".watchdog.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer logF.Close()
	cmd := exec.Command(exe, "playground-watchdog", clusterName)
	cmd.Stdout, cmd.Stderr = logF, logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}

func newPlaygroundWatchdogCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "playground-watchdog <cluster>",
		Short:  "Stop a playground at its time limit (started by astrona run)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
			defer stop()
			return watchPlayground(ctx, args[0], time.Minute)
		},
	}
}

// watchPlayground waits until the playground's deadline, checking every
// tick that it still runs under the same session (destroyed, replaced by
// a new run, or renewed — which starts its own watchdog: nothing to do).
// At the deadline it stops the clock as
// "time_limit" and destroys the cluster. A signal ends it quietly — that is
// how `astrona destroy` stops it.
func watchPlayground(ctx context.Context, clusterName string, tick time.Duration) error {
	st, err := labstate.Load(clusterName)
	if err != nil || st == nil || !st.Session.IsPlayground() {
		return err
	}
	sess := *st.Session
	deadline, err := time.Parse(time.RFC3339, sess.DeadlineAt)
	if err != nil {
		return fmt.Errorf("playground %s has no deadline: %w", clusterName, err)
	}
	fmt.Printf("%s watching %s until %s\n", time.Now().Format(time.RFC3339), clusterName, deadline.Format(time.RFC3339))
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
		if err == nil && (cur == nil || cur.Session == nil || cur.Session.ID != sess.ID) {
			return nil
		}
	}
	fmt.Printf("%s time limit reached: stopping %s\n", time.Now().Format(time.RFC3339), clusterName)
	stopPlaygroundClock(ctx, &sess, "time_limit")
	rep, err := ui.NewReporter("destroy", clusterName, false)
	if err != nil {
		return err
	}
	defer rep.Close()
	return destroyByName(clusterName, rep)
}

// stopPlaygroundClock tells the site the playground ended. The site keeps
// the first stop, so the watchdog's "time_limit" stands even if destroy
// reports again. Failures are warnings: the site stops the clock at the
// deadline anyway.
func stopPlaygroundClock(ctx context.Context, sess *labstate.Session, reason string) {
	client, store, _, err := accountDeps()
	if err != nil {
		ui.Warnf("couldn't stop the playground's clock: %s", err)
		return
	}
	creds, err := client.Active(ctx, store, sess.Site)
	if err != nil {
		ui.Warnf("couldn't stop the playground's clock on %s: %s — it stops by itself at the time limit", sess.Site, err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	noticeOtherSite(creds.Site, "the playground time")
	if err := client.StopLabSession(ctx, store, creds, sess.ID, reason); err != nil {
		ui.Warnf("couldn't stop the playground's clock on %s: %s — it stops by itself at the time limit", sess.Site, err)
	}
}

// endPlayground runs when a lab is forgotten (destroyed, or replaced by a
// fresh run): a playground's watchdog is stopped — unless that is us — and
// its clock is stopped as "destroyed".
func endPlayground(clusterName string) {
	st, err := labstate.Load(clusterName)
	if err != nil || st == nil || !st.Session.IsPlayground() {
		return
	}
	stopWatchdog(st.Session.WatchdogPID, clusterName)
	stopPlaygroundClock(context.Background(), st.Session, "destroyed")
}

// endOldSession runs before a lab is built again under the same name (run's
// start over, a full reset, or a run over what a vanished lab left behind):
// an old playground's watchdog — which would otherwise remove the new lab
// at the old deadline — and its clock are stopped, and the old session is
// forgotten. The rest (where the lab came from, the kubectl context to go
// back to) stays for the new run.
func endOldSession(clusterName string) {
	endPlayground(clusterName)
	if err := labstate.Update(clusterName, func(s *labstate.State) { s.Session = nil }); err != nil {
		ui.Warnf("could not forget the lab's previous session: %s", err)
	}
}

// watchdogCommandLine is pid's full command line (replaced in tests). The
// ps flags are the same on macOS and Linux; -ww stops it truncating.
var watchdogCommandLine = func(pid int) (string, error) {
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", fmt.Errorf("failed to read command line of pid %d: %w", pid, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// isWatchdogFor reports whether cmdline is `astrona playground-watchdog
// <clusterName>` — the process spawnWatchdog started, not one that reused
// its pid.
func isWatchdogFor(cmdline, clusterName string) bool {
	fields := strings.Fields(cmdline)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "playground-watchdog" && fields[i+1] == clusterName {
			return true
		}
	}
	return false
}

// stopWatchdog ends clusterName's playground watchdog — unless that is us.
// The pid is only signalled while it still leads its own session (Setsid)
// and runs that watchdog's command line: anything else reused the number
// and is left alone.
func stopWatchdog(pid int, clusterName string) {
	if pid <= 0 || pid == os.Getpid() || !hypervisor.ProcessAlive(pid) {
		return
	}
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		return
	}
	if cmdline, err := watchdogCommandLine(pid); err != nil || !isWatchdogFor(cmdline, clusterName) {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
}
