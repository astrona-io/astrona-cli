package labops

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/labshell"
	"astrona/internal/labstate"
	"astrona/internal/resources"
	"astrona/internal/ui"
)

// LabShellEnvVar is set inside `astrona shell` — guards against nesting
// and lets a user show the lab in their prompt.
const LabShellEnvVar = "ASTRONA_LAB"

// kubeContexts is the user's own kubeconfig (replaced in tests).
var kubeContexts = func() (labstate.Contexts, error) { return cluster.NewKubectlContexts() }

// LabSource is where a lab's config was named from: -c/--file, --git and
// --git-ref, or a catalog lab. cmd builds it from its flags.
type LabSource struct {
	Config, File, Git, GitRef, Catalog string
}

// RememberedSource is what `astrona submit` needs to find the lab's config
// again without being told. A local path is made absolute so it still
// works from another directory.
func RememberedSource(src LabSource) *labstate.Source {
	out := &labstate.Source{Config: src.Config, File: src.File, Git: src.Git, GitRef: src.GitRef, Catalog: src.Catalog}
	if out.Git == "" && !strings.HasPrefix(out.Config, "http://") && !strings.HasPrefix(out.Config, "https://") {
		if abs, err := filepath.Abs(out.Config); err == nil {
			out.Config = abs
		}
	}
	if out.File == "config.yaml" {
		out.File = ""
	}
	return out
}

// SourceHint is how to name a remembered lab on the command line.
func SourceHint(src *labstate.Source) string {
	if src == nil {
		return "(started by an older astrona — name its config with -c)"
	}
	return SourceCommand("submit", src)
}

// SourceCommand is `astrona <verb>` for the lab src names, e.g. the run
// command that starts it again. Arguments are quoted only when a shell
// would split or expand them.
func SourceCommand(verb string, src *labstate.Source) string {
	s := "astrona " + verb
	switch {
	case src.Catalog != "":
		return s + " " + quoteArg(src.Catalog)
	case src.Git != "":
		s += " --git " + quoteArg(src.Git)
		if src.Config != "" && src.Config != "." {
			s += " -c " + quoteArg(src.Config)
		}
		if src.GitRef != "" {
			s += " --git-ref " + quoteArg(src.GitRef)
		}
	default:
		s += " -c " + quoteArg(src.Config)
	}
	if src.File != "" {
		s += " -f " + quoteArg(src.File)
	}
	return s
}

// quoteArg leaves plain paths, URLs and lab IDs as they are.
func quoteArg(s string) string {
	plain := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-:@+=,", r)
	}
	if s == "" || strings.IndexFunc(s, func(r rune) bool { return !plain(r) }) >= 0 {
		return ShellQuote(s)
	}
	return s
}

// RememberLab records, once a lab is up, where its config came from and
// the lab session it runs under (nil: none — e.g. a -c lab). The lab works
// without it, so callers treat an error as a warning.
func RememberLab(clusterName string, src *labstate.Source, localOnly bool, sess *labstate.Session) error {
	return labstate.Update(clusterName, func(s *labstate.State) {
		s.Source = src
		s.Session = sess
		s.LocalOnly = localOnly
		s.StartedAt = time.Now().UTC().Format(time.RFC3339)
	})
}

// SwitchToLab makes the lab's context current in the user's kubeconfig and
// remembers the one to go back to. On an error the lab is reached as with
// --keep-context.
func SwitchToLab(clusterName, labContext string) (*labstate.Switched, error) {
	kc, err := kubeContexts()
	if err != nil {
		return nil, err
	}
	sw, err := labstate.SwitchContext(kc, clusterName, labContext)
	if err != nil {
		return nil, err
	}
	return &sw, nil
}

// ReleaseLab puts back the kubectl context `astrona run` switched away
// from — only if the lab's context is still current — and says on w what it
// did. Call it before the lab's cluster is deleted.
func ReleaseLab(w io.Writer, clusterName string) {
	if os.Getenv(LabShellEnvVar) != "" {
		return // An inherited isolated kubeconfig is not the user's original context.
	}
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

// ForgetOptions are the caller's choices for ForgetLab.
type ForgetOptions struct {
	// KeepResources is `astrona destroy --keep-resources`: leave the lab's
	// copy of its resources for reference.
	KeepResources bool
	// EndSession ends the run's session (a playground's watchdog and clock,
	// a lab's attempt) before its memory is removed. nil: nothing to end.
	EndSession func(clusterName string)
}

// ForgetLab preserves completed-run history, then removes active lab memory
// after ending its session. Problems are warnings, written to w.
func ForgetLab(w io.Writer, clusterName string, opts ForgetOptions) {
	if err := labshell.Remove(context.Background(), clusterName); err != nil {
		ui.Fwarnf(w, "could not remove terminal workspace: %s", err)
	}
	if st, err := labstate.Load(clusterName); err != nil {
		ui.Fwarnf(w, "could not read completed run for history: %s", err)
	} else if err := labstate.RecordFinishedRun(clusterName, st, time.Now()); err != nil {
		ui.Fwarnf(w, "could not save completed run history: %s", err)
	}
	if opts.EndSession != nil {
		opts.EndSession(clusterName)
	}
	if err := labstate.Remove(clusterName); err != nil {
		ui.Fwarnf(w, "%s", err)
	}
	if !opts.KeepResources {
		if err := resources.Remove(clusterName); err != nil {
			ui.Fwarnf(w, "%s", err)
		}
	}
}

// PreviousContextLabel is how the context `astrona destroy` restores is
// shown.
func PreviousContextLabel(sw *labstate.Switched) string {
	if sw.PreviousUnset {
		return "(none)"
	}
	return fmt.Sprintf("%q", sw.Previous)
}
