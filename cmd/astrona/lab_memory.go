package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"astrona/internal/cluster"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

// kubeContexts is the user's own kubeconfig (replaced in tests).
var kubeContexts = func() (labstate.Contexts, error) { return cluster.NewKubectlContexts() }

// rememberedSource is where flags point: what `astrona submit` needs to find the
// lab's config again without being told. A local path is made absolute so
// it still works from another directory.
func rememberedSource(flags *rootFlags) *labstate.Source {
	src := &labstate.Source{Config: flags.configPath, File: flags.fileName, Git: flags.gitURL, GitRef: flags.gitRef, Catalog: flags.catalogLab}
	if src.Git == "" && !strings.HasPrefix(src.Config, "http://") && !strings.HasPrefix(src.Config, "https://") {
		if abs, err := filepath.Abs(src.Config); err == nil {
			src.Config = abs
		}
	}
	if src.File == "config.yaml" {
		src.File = ""
	}
	return src
}

// applySource points flags at a remembered lab's config, like
// applyCurrentLab does for `astrona use`.
func applySource(flags *rootFlags, src *labstate.Source) {
	flags.configPath, flags.gitURL, flags.gitRef, flags.catalogLab = src.Config, src.Git, src.GitRef, src.Catalog
	flags.fileName = "config.yaml"
	if src.File != "" {
		flags.fileName = src.File
	}
	flags.fromCurrent = false
}

// sourceHint is how to name a remembered lab on the command line.
func sourceHint(src *labstate.Source) string {
	switch {
	case src == nil:
		return "(started by an older astrona — name its config with -c)"
	case src.Catalog != "":
		return "astrona submit " + src.Catalog
	case src.Git != "":
		s := "astrona submit --git " + src.Git
		if src.Config != "" && src.Config != "." {
			s += " -c " + src.Config
		}
		if src.GitRef != "" {
			s += " --git-ref " + src.GitRef
		}
		return s
	default:
		return "astrona submit -c " + src.Config
	}
}

// rememberLab records, once a lab is up, where its config came from and
// the lab session it runs under (nil: none — e.g. a -c lab). Best effort:
// the lab works without it, so a failure is only a warning.
func rememberLab(clusterName string, flags *rootFlags, sess *labstate.Session) {
	err := labstate.Update(clusterName, func(s *labstate.State) {
		s.Source = rememberedSource(flags)
		s.Session = sess
	})
	if err != nil {
		ui.Warnf("could not remember this lab (%s) — `astrona submit` needs it named, and results won't reach the lab page", err)
	}
}

// switchToLab makes the lab's context current in the user's kubeconfig and
// remembers the one to go back to. nil when it couldn't (a warning says
// why) — the lab is then reached as with --keep-context.
func switchToLab(clusterName, labContext string) *labstate.Switched {
	kc, err := kubeContexts()
	if err == nil {
		var sw labstate.Switched
		if sw, err = labstate.SwitchContext(kc, clusterName, labContext); err == nil {
			return &sw
		}
	}
	ui.Warnf("kubectl was not switched to this lab: %s", err)
	return nil
}

// releaseLab puts back the kubectl context `astrona run` switched away
// from — only if the lab's context is still current — and says what it
// did. Call it before the lab's cluster is deleted.
func releaseLab(w io.Writer, clusterName string) {
	st, err := labstate.Load(clusterName)
	if err != nil || st == nil || st.KubeContext == nil {
		return
	}
	kc, err := kubeContexts()
	if err != nil {
		fmt.Fprintf(w, "Your kubectl context was not restored (%s) — set it with: kubectl config use-context <name>\n", err)
		return
	}
	res, err := labstate.RestoreContext(kc, clusterName)
	if err != nil {
		fmt.Fprintf(w, "Your kubectl context was not restored: %s\n", err)
		return
	}
	switch res.Outcome {
	case labstate.Restored:
		fmt.Fprintf(w, "kubectl points at %q again (your context before the lab).\n", res.Previous)
	case labstate.RestoredUnset:
		fmt.Fprintf(w, "kubectl has no current context again, as before the lab.\n")
	case labstate.MovedOn:
		cur := "none"
		if res.Current != "" {
			cur = fmt.Sprintf("%q", res.Current)
		}
		fmt.Fprintf(w, "kubectl's current context is %s now, not the lab's — left it as it is.\n", cur)
	case labstate.PreviousGone:
		fmt.Fprintf(w, "Your previous kubectl context %q no longer exists — left kubectl's current context alone. Pick one with: kubectl config use-context <name>\n", res.Previous)
	}
}

// forgetLab removes everything remembered about a destroyed lab — first
// ending a timed playground (its watchdog and its clock).
func forgetLab(clusterName string) {
	endPlayground(clusterName)
	if err := labstate.Remove(clusterName); err != nil {
		ui.Warnf("%s", err)
	}
}

// previousContextLabel is how the context `astrona destroy` restores is
// shown.
func previousContextLabel(sw *labstate.Switched) string {
	if sw.PreviousUnset {
		return "(none)"
	}
	return fmt.Sprintf("%q", sw.Previous)
}
