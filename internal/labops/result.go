package labops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"astrona/internal/account"
	"astrona/internal/labstate"
	"astrona/internal/outbox"
	"astrona/internal/ui"
)

// SignInHint is what result sending says when nobody is signed in.
const SignInHint = "Sign in (astrona login) and run the lab again to have results show on the lab page."

// ResultOutcome is what became of a graded result sent to the lab page.
type ResultOutcome int

const (
	ResultNotSent ResultOutcome = iota // refused, or nowhere to send it
	ResultSent                         // the lab page has it
	ResultQueued                       // kept in the outbox; sent when Astrona is reachable
)

// SendResult is DeliverResult for a result graded now, reporting whether
// the lab page has it.
func SendResult(ctx context.Context, w, warn io.Writer, clusterName string, src *labstate.Source, result any) bool {
	return DeliverResult(ctx, w, warn, clusterName, src, result, time.Now()) == ResultSent
}

// newResultKey is a submit's idempotency key: random, so a repeat delivery
// of the same submit (from the outbox) is recognized and a new submit never
// is.
func newResultKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// DeliverResult sends a graded result (the object `astrona submit -o json`
// prints, as it goes to the lab page) to the lab page of the session the
// lab was started under, graded at gradedAt, under a new idempotency key.
// When Astrona can't be reached, fails, or needs a new sign-in, the result
// goes to the outbox with that key and is sent later; when the session
// already has signals waiting there, it goes behind them.
// Nothing is sent without a remembered session for the site the CLI is
// signed in to. Every outcome is a line on w, and every warning a [WARN]
// line on warn; nothing goes to stdout, and none of them changes the exit
// code. src is where the graded config came from: a result is only sent to
// a session started from that same config — another lab of the same name
// (-c ./other) never reaches the catalog lab's page.
func DeliverResult(ctx context.Context, w, warn io.Writer, clusterName string, src *labstate.Source, result any, gradedAt time.Time) ResultOutcome {
	catalogLab := src.Catalog
	lab := catalogLab
	if lab == "" {
		lab = clusterName
	}
	st, err := labstate.Load(clusterName)
	if err != nil {
		ui.Fwarnf(warn, "the result was not sent to Astrona: %s", err)
		return ResultNotSent
	}
	var sess *labstate.Session
	if st != nil {
		if st.LocalOnly {
			fmt.Fprintln(w, "This run is local only. No result was sent to Astrona.")
			return ResultNotSent
		}
		sess = st.Session
	}
	if sess == nil && catalogLab == "" {
		return ResultNotSent // a lab from your own files: nothing to send it to
	}
	if sess != nil && !SameSource(st.Source, src) {
		fmt.Fprintf(w, "%s runs a lab session for another config than the one graded here — the result was not sent. "+
			"Send it from that config: %s\n", clusterName, SourceHint(st.Source))
		return ResultNotSent
	}

	client, store, site, err := AccountDeps()
	if err != nil {
		ui.Fwarnf(warn, "the result was not sent to Astrona: %s", err)
		return ResultNotSent
	}
	if sess != nil && sess.Site != site {
		fmt.Fprintf(w, "This lab's session is on %s, but astrona is signed in to %s — the result was not sent.\n", sess.Site, site)
		return ResultNotSent
	}
	post := account.ResultPost{Result: result, Usage: RunUsageReport(clusterName, st), Key: newResultKey(), At: gradedAt.UTC()}
	creds, err := client.Active(ctx, store, site)
	if err != nil && sess != nil {
		// A remembered session is enough to retain its grade. Renewing an
		// expired sign-in needs the network too, so it can fail while offline.
		return queueResult(ctx, w, warn, sess, post, err)
	}
	if errors.Is(err, account.ErrSignedOut) {
		fmt.Fprintln(w, SignInHint)
		return ResultNotSent
	}
	if err != nil {
		ui.Fwarnf(warn, "the result was not sent to Astrona: %s", err)
		return ResultNotSent
	}
	if sess == nil {
		fmt.Fprintf(w, "This lab wasn't started with a lab session, so the result was not sent — run it again (astrona run %s) to have results show on the lab page.\n", catalogLab)
		return ResultNotSent
	}

	NoticeOtherSite(warn, creds.Site, "your lab results")
	if waiting, _ := outbox.PendingFor(sess.ID); waiting > 0 {
		// Signals for this session wait in the outbox: the result goes
		// behind them, so the site gets them in the order they happened.
		return queueResult(ctx, w, warn, sess, post, nil)
	}
	lr, err := client.PostLabResult(ctx, store, creds, sess.ID, post)
	switch {
	case err == nil:
		fmt.Fprintf(w, "Sent to your lab page (attempt %d): %s\n", lr.Attempt, resultPage(lr.URL, sess, site))
		return ResultSent
	case errors.Is(err, account.ErrSessionGone):
		fmt.Fprintln(w, "This lab's session has expired or isn't yours — the result was not sent. Run the lab again to track a new attempt.")
	case errors.Is(err, account.ErrAttemptFinished):
		// A pass ends the attempt; a site before several submits per
		// attempt ends it at the first result. Either way it is final.
		fmt.Fprintf(w, "The result was not sent: %s\nStart a new tracked attempt with: astrona reset %s\n",
			siteDetail(err, account.ErrAttemptFinished, "This lab attempt is already finished."), lab)
	case errors.Is(err, account.ErrSessionExpired):
		fmt.Fprintf(w, "The result was not sent: %s\nStart a new attempt with: astrona reset %s   (or: astrona destroy %s, then astrona run %s)\n",
			siteDetail(err, account.ErrSessionExpired, "This lab session has expired — run the lab again to start a new attempt."), lab, lab, lab)
	case errors.Is(err, account.ErrTooManyResults):
		fmt.Fprintf(w, "The result was not sent: %s\nStart a new tracked attempt with: astrona reset %s\n",
			siteDetail(err, account.ErrTooManyResults, "This lab attempt already has as many results as Astrona keeps."), lab)
	case account.Permanent(err):
		ui.Fwarnf(warn, "%s refused the result: %s — it is still recorded here (astrona submit --history)", site, err)
	default:
		// Not reached, a server error, or a sign-in to renew: keep it and
		// send it later under the same key.
		return queueResult(ctx, w, warn, sess, post, err)
	}
	return ResultNotSent
}

// siteDetail is the site's own wording of a refusal wrapping sentinel, or
// fallback when it gave none.
func siteDetail(err, sentinel error, fallback string) string {
	detail := strings.TrimPrefix(err.Error(), sentinel.Error()+": ")
	if detail == err.Error() {
		return fallback
	}
	return detail
}

// queueResult keeps post in the outbox for sess, under the same key. With
// failed nil (other signals wait before it) it tries to send them all at
// once; after a failed send it does not try again here. What still waits
// goes out with the background flusher.
func queueResult(ctx context.Context, w, warn io.Writer, sess *labstate.Session, post account.ResultPost, failed error) ResultOutcome {
	raw, err := json.Marshal(post.Result)
	if err == nil {
		ev := outbox.Event{Kind: outbox.Result, At: post.At, Key: post.Key, Result: raw, Usage: post.Usage}
		status := outbox.Status{State: outbox.Offline}
		if errors.Is(failed, account.ErrSignedOut) {
			status.State = outbox.SignedOut
		}
		if failed != nil {
			err = outbox.Add(sess.Site, sess.ID, ev)
		} else {
			status, err = QueueSignal(ctx, sess, ev)
		}
		if err == nil {
			switch status.State {
			case outbox.Online:
				fmt.Fprintf(w, "Sent to your lab page: %s\n", sess.URL)
				return ResultSent
			case outbox.SignedOut:
				ui.Fwarnf(warn, "the result is queued on this machine — sign in again (astrona login) to send it to %s", sess.Site)
			default:
				ui.Fwarnf(warn, "the result couldn't reach %s — it is queued on this machine and sent when Astrona is reachable", sess.Site)
			}
			StartOutboxFlusher()
			return ResultQueued
		}
	}
	if failed != nil {
		err = fmt.Errorf("%w (and it could not be queued: %w)", failed, err)
	}
	ui.Fwarnf(warn, "the result could not be sent to %s: %s — it is still recorded here (astrona submit --history)", sess.Site, err)
	return ResultNotSent
}

// resultPage is the lab page to show for a sent result: the site's answer
// when it is a page on that site, else the page the session started with.
func resultPage(raw string, sess *labstate.Session, site string) string {
	if u, err := account.CheckHTTPURL(raw); err == nil && account.SameSite(u, site) {
		return u.String()
	}
	return sess.URL
}

// SameSource reports whether the config being graded (b) is the one the
// lab was started from (a, remembered by run): the same catalog lab, or
// the same -c/--file/--git/--git-ref. Nothing remembered never matches.
func SameSource(a, b *labstate.Source) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Catalog != "" || b.Catalog != "" {
		return SameCatalogName(a.Catalog, b.Catalog)
	}
	return a.Config == b.Config && a.File == b.File && a.Git == b.Git && a.GitRef == b.GitRef
}

// RunUsageReport is the summary of what a run used so far, for submit and
// destroy; nil when it was never measured or usage samples are turned off in
// settings.
func RunUsageReport(clusterName string, st *labstate.State) *account.UsageReport {
	if st == nil || st.StartedAt == "" || !UsageSamplesAllowed() {
		return nil
	}
	u, err := labstate.ReadUsage(clusterName, st.StartedAt)
	if err != nil || u.Samples == 0 {
		return nil
	}
	return &account.UsageReport{
		AverageCPUCores:    u.AverageCPU / 100,
		AverageMemoryBytes: int64(u.AverageMemory),
		PeakCPUCores:       u.PeakCPU / 100,
		PeakMemoryBytes:    int64(u.PeakMemory),
		Samples:            u.Samples,
	}
}
