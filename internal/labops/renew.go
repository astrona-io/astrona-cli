package labops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"astrona/internal/account"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// PlaygroundExists reports whether any part of a playground's lab still
// exists: its kind cluster, its qemu VM, or a VM of it ("<lab>-<vm>").
// A container engine that can't be asked counts as existing: the playground
// may still be there, and a false "gone" would forget it. Replaced in tests.
var PlaygroundExists = func(name string) bool {
	if hypervisor.StateExists(name) {
		return true
	}
	if ok, err := cluster.Lookup(name); ok || err != nil {
		return true
	}
	rows, _, _ := CollectQEMURows() // an unreadable qemu dir just means no qemu labs here
	for _, r := range rows {
		if strings.HasPrefix(r.Name, name+"-") {
			return true
		}
	}
	return false
}

// RunningPlaygrounds lists the labs whose remembered session is a
// playground: all of them, or the one name matches — its cluster name (with
// or without "astro-"), or its catalog name (with or without the owner).
// One whose lab is gone (removed outside astrona) is forgotten with forget
// — its watchdog and clock stopped — rather than renewed.
func RunningPlaygrounds(name string, forget func(clusterName string)) ([]string, error) {
	names, err := labstate.Names()
	if err != nil {
		return nil, err
	}
	want := strings.Trim(name, "/")
	var out []string
	for _, c := range names {
		st, err := labstate.Load(c)
		if err != nil || st == nil || !st.Session.IsPlayground() {
			continue
		}
		if want != "" && c != config.NormalizeClusterName(want) && !SameCatalogName(st.Session.Lab, want) {
			continue
		}
		if !PlaygroundExists(c) {
			forget(c)
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// RenewResult is what a renewed playground got.
type RenewResult struct {
	Cluster string
	// Banked is the playground time so far the site sent to the account,
	// rounded to the minute.
	Banked time.Duration
	// Deadline is when the playground now stops and is removed.
	Deadline time.Time
}

// RenewPlayground has the site bank the playground's time so far and start
// a new session with a full time limit, remembers that session and its
// deadline, and replaces the watchdog with one for it. Notices and
// warnings go to warn; what to print about the result is the caller's.
func RenewPlayground(ctx context.Context, warn io.Writer, clusterName string) (*RenewResult, error) {
	release, err := labstate.LockMutation(clusterName)
	if err != nil {
		return nil, err
	}
	defer release()
	st, err := labstate.Load(clusterName)
	if err != nil || st == nil || !st.Session.IsPlayground() {
		return nil, errors.New("no playground session is remembered for it")
	}
	client, store, _, err := AccountDeps()
	if err != nil {
		return nil, err
	}
	creds, err := client.Active(ctx, store, st.Session.Site)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	NoticeOtherSite(warn, creds.Site, "the playground time so far")
	renewed, err := client.RenewLabSession(ctx, store, creds, st.Session.ID)
	if errors.Is(err, account.ErrPlaygroundStopped) {
		return nil, fmt.Errorf("%w — `astrona destroy %s`, then run it again", err, clusterName)
	}
	if err != nil {
		return nil, err
	}
	renewed.DeadlineAt = ClampDeadline(renewed.DeadlineAt, time.Now(), 0)
	deadline, err := time.Parse(time.RFC3339, renewed.DeadlineAt)
	if err != nil {
		return nil, fmt.Errorf("the site sent no new deadline: %w", err)
	}
	if err := labstate.Update(clusterName, func(s *labstate.State) {
		if s.Session != nil && s.Session.ID == st.Session.ID {
			s.Session.ID = renewed.ID
			s.Session.ExpiresAt = renewed.ExpiresAt
			s.Session.DeadlineAt = renewed.DeadlineAt
			s.Session.MaxMinutes = renewed.MaxMinutes
		}
	}); err != nil {
		return nil, fmt.Errorf("renewed on the site, but couldn't save the new deadline: %w", err)
	}
	// A fresh watchdog for the new deadline: the running one may be gone
	// (a reboot) or come from an astrona that doesn't follow renewals.
	StopWatchdog(st.Session.WatchdogPID, clusterName)
	if pid, err := SpawnWatchdog(clusterName); err != nil {
		ui.Fwarnf(warn, "couldn't restart the playground's timer (%s) — run `astrona destroy %s` when you are done", err, clusterName)
	} else {
		_ = labstate.Update(clusterName, func(s *labstate.State) {
			if s.Session != nil {
				s.Session.WatchdogPID = pid
			}
		})
	}
	return &RenewResult{
		Cluster:  clusterName,
		Banked:   (time.Duration(renewed.BankedSeconds) * time.Second).Round(time.Minute),
		Deadline: deadline,
	}, nil
}
