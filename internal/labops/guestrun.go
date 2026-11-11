package labops

import (
	"context"
	"path"
	"time"

	"astrona/internal/account"
	"astrona/internal/guestrun"
	"astrona/internal/labstate"
)

// CLIVersion is this build's version, sent with a guest run report. cmd sets
// it at start-up.
var CLIVersion = "developer"

// isGuestPlayground reports whether a guest run's catalog lab is a module's
// playground.
func isGuestPlayground(src *labstate.Source) bool {
	return IsPlaygroundName(src.Catalog) || path.Base(src.Config) == "playground"
}

// PrepareGuestRun makes sure the anonymous install id exists before a guest
// catalog run starts, so the first lab or playground after install creates
// it without asking. Best effort: an error only means the report is made
// with a new id later.
func PrepareGuestRun() {
	_, _ = guestrun.InstallID()
}

// guestReport is the one anonymous report a guest run sends once it
// counts: a lab submitted, a playground destroyed. nil: nothing to send
// (not a guest run, --local-only, or a lab never submitted).
func guestReport(lab string, st *labstate.State, now time.Time) (*guestrun.Report, error) {
	if st == nil || !st.Guest || st.LocalOnly || st.Session != nil || st.Source == nil || st.Source.Catalog == "" || st.StartedAt == "" {
		return nil, nil
	}
	started, err := time.Parse(time.RFC3339, st.StartedAt)
	if err != nil {
		return nil, nil // an unreadable start: nothing sensible to report
	}
	r := &guestrun.Report{Lab: st.Source.Catalog, StartedAt: started.UTC(), CLIVersion: CLIVersion}
	if isGuestPlayground(st.Source) {
		r.Kind, r.EndReason, r.FinishedAt = guestrun.Playground, "destroyed", now.UTC()
	} else {
		sub, err := labstate.ReadSubmission(lab, st.StartedAt)
		if err != nil || sub == nil {
			return nil, err // a lab counts only once it is submitted
		}
		r.Kind, r.EndReason, r.FinishedAt = guestrun.Lab, "submitted", sub.At.UTC()
	}
	if r.FinishedAt.Before(r.StartedAt) {
		r.FinishedAt = r.StartedAt
	}
	used := r.FinishedAt.Sub(r.StartedAt)
	r.UsedSeconds = int(min(used, guestrun.MaxUsed) / time.Second)
	_, _, site, err := AccountDeps()
	if err != nil {
		return nil, err
	}
	id, err := guestrun.InstallID()
	if err != nil {
		return nil, err
	}
	r.Site, r.InstallID, r.IdempotencyKey = site, id, guestrun.NewKey()
	return r, nil
}

// queueGuestRun queues lab's guest report, if its run counts, and starts the
// background sender. Called by ForgetLab before the lab's memory goes.
func queueGuestRun(lab string, st *labstate.State) error {
	r, err := guestReport(lab, st, time.Now())
	if err != nil || r == nil {
		return err
	}
	if err := guestrun.Queue(*r); err != nil {
		return err
	}
	StartOutboxFlusher()
	return nil
}

// FlushGuestRuns sends the guest run reports waiting; it returns how many
// are left.
func FlushGuestRuns(ctx context.Context) (int, error) {
	client := account.NewClient()
	return guestrun.Flush(ctx, func(ctx context.Context, site string, r guestrun.Report) error {
		ctx, cancel := context.WithTimeout(ctx, SignalTimeout)
		defer cancel()
		return client.PostGuestRun(ctx, site, r)
	}, account.Permanent)
}
