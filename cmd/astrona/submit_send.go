package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"astrona/internal/apphome"
	"astrona/internal/labops"
	"astrona/internal/labstate"
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
		if _, linked := owners[r.Name]; linked || strings.HasPrefix(r.Name, apphome.TestClusterPrefix()) || !isRunning(r) {
			continue
		}
		add(r.Name)
	}
	for _, r := range qemuRows {
		if strings.HasPrefix(r.Name, apphome.TestClusterPrefix()) {
			continue
		}
		add(labOfVM(r.Name, known))
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

const signInHint = labops.SignInHint

// sendResult sends a graded result (the object `astrona submit -o json`
// prints) to the lab page of the session the lab was started under, and
// reports whether it was recorded there (labops.SendResult). Every outcome
// is a line on w and warnings go to stderr — never stdout, so `submit -o
// json` stays clean; none of them changes the exit code. Check texts are
// capped as they go to the lab page.
func sendResult(ctx context.Context, w io.Writer, clusterName string, src *labstate.Source, result submissionResult) bool {
	return labops.SendResult(ctx, w, os.Stderr, clusterName, src, capCheckTexts(result))
}

// sameSource reports whether the config being graded (b) is the one the
// lab was started from (a, remembered by run).
func sameSource(a, b *labstate.Source) bool { return labops.SameSource(a, b) }

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
