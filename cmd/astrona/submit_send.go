package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"astrona/internal/account"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// labConfigHere reports whether flags' config location (the current
// directory by default) holds a lab config.
func labConfigHere(flags *rootFlags) bool {
	if flags.gitURL != "" {
		return true
	}
	_, err := os.Stat(filepath.Join(flags.configPath, flags.fileName))
	return err == nil
}

// runningLabNames lists the running astrona labs the way `astrona list`
// finds them — kind clusters and live qemu VMs — leaving out linked
// clusters (they belong to their lab) and `astrona test` copies. The VMs
// of a multi-VM lab ("<lab>-<vm>") count as their lab when it is
// remembered. Replaced in tests.
var runningLabNames = func() []string {
	qemuRows, _, _ := collectQEMURows() // an unreadable qemu dir just means no qemu labs here
	kindRows := collectKindRows()
	owners := linkedClusterOwners()
	known, _ := labstate.Names()

	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, r := range kindRows {
		if _, linked := owners[r.name]; linked || strings.HasPrefix(r.name, "astro-test-") || !isRunning(r) {
			continue
		}
		add(r.name)
	}
	for _, r := range qemuRows {
		if strings.HasPrefix(r.name, "astro-test-") {
			continue
		}
		add(labOfVM(r.name, known))
	}
	return names
}

// labOfVM maps a qemu VM's name to the remembered lab it belongs to: the
// lab itself (single-VM) or the longest remembered lab "<lab>-<vm>"
// starts with; else the VM's own name.
func labOfVM(vm string, known []string) string {
	best := ""
	for _, k := range known {
		if (vm == k || strings.HasPrefix(vm, k+"-")) && len(k) > len(best) {
			best = k
		}
	}
	if best == "" {
		return vm
	}
	return best
}

// pickRunningLab is the lab `astrona submit` grades when none is named:
// the only running one, found again through what `astrona run`
// remembered. No lab running returns no source, so the usual "no lab
// here" error follows; several, or one whose config isn't known, is an
// error saying how to name it.
func pickRunningLab(names []string, load func(string) (*labstate.State, error)) (string, *labstate.Source, error) {
	source := func(name string) *labstate.Source {
		st, err := load(name)
		if err != nil || st == nil {
			return nil
		}
		return st.Source
	}
	switch len(names) {
	case 0:
		return "", nil, nil
	case 1:
		src := source(names[0])
		if src == nil {
			return "", nil, fmt.Errorf("%s is running, but astrona doesn't know which config it was started from "+
				"(it was started by an older astrona) — name it: astrona submit -c <lab-dir>", names[0])
		}
		return names[0], src, nil
	}
	var lines []string
	for _, n := range names {
		lines = append(lines, fmt.Sprintf("  %s    %s", n, sourceHint(source(n))))
	}
	return "", nil, fmt.Errorf("several labs are running — name the one to submit:\n%s", strings.Join(lines, "\n"))
}

const signInHint = "Sign in (astrona login) and run the lab again to have results show on the lab page."

// sendResult sends a graded result (the object `astrona submit -o json`
// prints) to the lab page of the session the lab was started under, and
// reports whether it was recorded there. Nothing is sent without a
// remembered session for the site the CLI is signed in to. Every outcome
// is a line on w; none of them changes the exit code. src is where the
// graded config came from: a result is only sent to a session started
// from that same config — another lab of the same name (-c ./other) never
// reaches the catalog lab's page.
func sendResult(ctx context.Context, w io.Writer, clusterName string, src *labstate.Source, result submissionResult) bool {
	catalogLab := src.Catalog
	lab := catalogLab
	if lab == "" {
		lab = clusterName
	}
	st, err := labstate.Load(clusterName)
	if err != nil {
		ui.Warnf("the result was not sent to Astrona: %s", err)
		return false
	}
	var sess *labstate.Session
	if st != nil {
		sess = st.Session
	}
	if sess == nil && catalogLab == "" {
		return false // a lab from your own files: nothing to send it to
	}
	if sess != nil && !sameSource(st.Source, src) {
		fmt.Fprintf(w, "%s runs a lab session for another config than the one graded here — the result was not sent. "+
			"Send it from that config: %s\n", clusterName, sourceHint(st.Source))
		return false
	}

	client, store, site, err := accountDeps()
	if err != nil {
		ui.Warnf("the result was not sent to Astrona: %s", err)
		return false
	}
	if sess != nil && sess.Site != site {
		fmt.Fprintf(w, "This lab's session is on %s, but astrona is signed in to %s — the result was not sent.\n", sess.Site, site)
		return false
	}
	creds, err := client.Active(ctx, store, site)
	if errors.Is(err, account.ErrSignedOut) {
		fmt.Fprintln(w, signInHint)
		return false
	}
	if err != nil {
		ui.Warnf("the result was not sent to Astrona: %s", err)
		return false
	}
	if sess == nil {
		fmt.Fprintf(w, "This lab wasn't started with a lab session, so the result was not sent — run it again (astrona run %s) to have results show on the lab page.\n", catalogLab)
		return false
	}

	noticeOtherSite(creds.Site, "your lab results")
	lr, err := client.SendLabResult(ctx, store, creds, sess.ID, capCheckTexts(result))
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
		fmt.Fprintln(w, signInHint)
	default:
		ui.Warnf("the result could not be sent to %s: %s — it is still recorded here (astrona submit --history)", site, err)
	}
	return false
}

// resultPage is the lab page to show for a sent result: the site's answer
// when it is a page on that site, else the page the session started with.
func resultPage(raw string, sess *labstate.Session, site string) string {
	if u, err := validateOpenURL(raw); err == nil && account.SameSite(u, site) {
		return u.String()
	}
	return sess.URL
}

// sameSource reports whether the config being graded (b) is the one the
// lab was started from (a, remembered by run): the same catalog lab, or
// the same -c/--file/--git/--git-ref. Nothing remembered never matches.
func sameSource(a, b *labstate.Source) bool {
	if a == nil || b == nil {
		return false
	}
	if a.Catalog != "" || b.Catalog != "" {
		return sameCatalogName(a.Catalog, b.Catalog)
	}
	return a.Config == b.Config && a.File == b.File && a.Git == b.Git && a.GitRef == b.GitRef
}

// maxSentCheckText caps each check's message and hint sent to the lab page.
const maxSentCheckText = 2048

// capCheckTexts is result as it is sent to the lab page: each check's
// message and hint — script output, possibly huge — without control
// characters (newlines and tabs stay) and capped at maxSentCheckText
// bytes. The result printed and recorded here is unchanged.
func capCheckTexts(result submissionResult) submissionResult {
	clean := func(s string) string {
		s = strings.Map(func(r rune) rune {
			if r == '\n' || r == '\t' || unicode.IsPrint(r) {
				return r
			}
			return -1
		}, s)
		if len(s) <= maxSentCheckText {
			return s
		}
		cut := maxSentCheckText
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return s[:cut] + "…"
	}
	checks := make([]checkJSON, len(result.Checks))
	for i, c := range result.Checks {
		c.Message, c.Hint = clean(c.Message), clean(c.Hint)
		checks[i] = c
	}
	result.Checks = checks
	return result
}

// followUp is what follows a graded, possibly sent, result.
type followUp int

const (
	followNothing followUp = iota
	followHint             // keep the lab, say how to remove it later
	followAsk              // ask whether to delete the lab now
)

// submitFollowUp decides what follows a result: only a pass that reached
// the lab page offers to delete the lab — asked in a terminal, a hint
// under -o json or without one; --keep, or a lab with
// teardown.keepCluster, leaves it be. A failing result never asks: the
// clock keeps running, fix it and submit again.
func submitFollowUp(pass, sent, keep, jsonOut, tty, keepCluster bool) followUp {
	switch {
	case !pass || !sent || keep || keepCluster:
		return followNothing
	case jsonOut || !tty:
		return followHint
	default:
		return followAsk
	}
}

// offerDestroy follows a passing result that reached the lab page: in a
// terminal (ask) it asks whether to delete the lab now — Enter or y
// deletes it — otherwise, or on n, the lab is kept and how to remove it
// later is printed on w. A failed delete is only a warning: the result
// stands.
func offerDestroy(in io.Reader, prompt, w io.Writer, ask bool, lab string, destroy func() error) {
	keptHint := fmt.Sprintf("Kept. Remove it later with: astrona destroy %s\n", lab)
	if !ask || !confirmDefaultYes(in, prompt, "Delete the lab cluster now?") {
		fmt.Fprint(w, keptHint)
		return
	}
	if err := destroy(); err != nil {
		ui.Warnf("could not delete the lab: %s — try: astrona destroy %s", err, lab)
	}
}

// confirmDefaultYes asks question on in/out; Enter, "y" or "yes" confirms.
func confirmDefaultYes(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s [Y/n] ", question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false // no answer at all (closed input): keep
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}
