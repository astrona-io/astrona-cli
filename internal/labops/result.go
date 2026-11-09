package labops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"astrona/internal/account"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// SignInHint is what result sending says when nobody is signed in.
const SignInHint = "Sign in (astrona login) and run the lab again to have results show on the lab page."

// SendResult sends a graded result (the object `astrona submit -o json`
// prints, as it goes to the lab page) to the lab page of the session the
// lab was started under, and reports whether it was recorded there.
// Nothing is sent without a remembered session for the site the CLI is
// signed in to. Every outcome is a line on w, and every warning a [WARN]
// line on warn; nothing goes to stdout, and none of them changes the exit
// code. src is where the graded config came from: a result is only sent to
// a session started from that same config — another lab of the same name
// (-c ./other) never reaches the catalog lab's page.
func SendResult(ctx context.Context, w, warn io.Writer, clusterName string, src *labstate.Source, result any) bool {
	catalogLab := src.Catalog
	lab := catalogLab
	if lab == "" {
		lab = clusterName
	}
	st, err := labstate.Load(clusterName)
	if err != nil {
		ui.Fwarnf(warn, "the result was not sent to Astrona: %s", err)
		return false
	}
	var sess *labstate.Session
	if st != nil {
		if st.LocalOnly {
			fmt.Fprintln(w, "This run is local only. No result was sent to Astrona.")
			return false
		}
		sess = st.Session
	}
	if sess == nil && catalogLab == "" {
		return false // a lab from your own files: nothing to send it to
	}
	if sess != nil && !SameSource(st.Source, src) {
		fmt.Fprintf(w, "%s runs a lab session for another config than the one graded here — the result was not sent. "+
			"Send it from that config: %s\n", clusterName, SourceHint(st.Source))
		return false
	}

	client, store, site, err := AccountDeps()
	if err != nil {
		ui.Fwarnf(warn, "the result was not sent to Astrona: %s", err)
		return false
	}
	if sess != nil && sess.Site != site {
		fmt.Fprintf(w, "This lab's session is on %s, but astrona is signed in to %s — the result was not sent.\n", sess.Site, site)
		return false
	}
	creds, err := client.Active(ctx, store, site)
	if errors.Is(err, account.ErrSignedOut) {
		fmt.Fprintln(w, SignInHint)
		return false
	}
	if err != nil {
		ui.Fwarnf(warn, "the result was not sent to Astrona: %s", err)
		return false
	}
	if sess == nil {
		fmt.Fprintf(w, "This lab wasn't started with a lab session, so the result was not sent — run it again (astrona run %s) to have results show on the lab page.\n", catalogLab)
		return false
	}

	NoticeOtherSite(warn, creds.Site, "your lab results")
	lr, err := client.SendLabResult(ctx, store, creds, sess.ID, result, RunUsageReport(clusterName, st))
	switch {
	case err == nil:
		fmt.Fprintf(w, "Sent to your lab page (attempt %d): %s\n", lr.Attempt, resultPage(lr.URL, sess, site))
		return true
	case errors.Is(err, account.ErrSessionGone):
		fmt.Fprintln(w, "This lab's session has expired or isn't yours — the result was not sent. Run the lab again to track a new attempt.")
	case errors.Is(err, account.ErrAttemptFinished):
		detail := strings.TrimPrefix(err.Error(), account.ErrAttemptFinished.Error()+": ")
		if detail == err.Error() {
			detail = "This lab attempt is already finished."
		}
		fmt.Fprintf(w, "The result was not sent: %s\n", detail)
	case errors.Is(err, account.ErrSessionExpired):
		detail := strings.TrimPrefix(err.Error(), account.ErrSessionExpired.Error()+": ")
		if detail == err.Error() {
			detail = "This lab session has expired — run the lab again to start a new attempt."
		}
		fmt.Fprintf(w, "The result was not sent: %s\nStart a new attempt with: astrona reset %s   (or: astrona destroy %s, then astrona run %s)\n", detail, lab, lab, lab)
	case errors.Is(err, account.ErrSignedOut):
		fmt.Fprintln(w, SignInHint)
	default:
		ui.Fwarnf(warn, "the result could not be sent to %s: %s — it is still recorded here (astrona submit --history)", site, err)
	}
	return false
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
// destroy; nil when it was never measured.
func RunUsageReport(clusterName string, st *labstate.State) *account.UsageReport {
	if st == nil || st.StartedAt == "" {
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
