package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/portforward"
	"astrona/internal/proctor"

	"github.com/spf13/cobra"
)

// labStatus is everything `astrona status` shows about one lab, gathered
// from state astrona already keeps (no new state of its own).
type labStatus struct {
	row      labRow
	cfg      *config.LabConfig // nil when no lab config was found
	forwards []portforward.Forward
	links    []cluster.LinkState
	linkRows map[string]labRow // linked cluster → its row (status), when running
	exam     *exam.State
	attempts []proctor.Attempt
	now      time.Time
}

// findLabRow returns the `astrona list` row for name (with live kind
// health), or false if no such lab is running.
func findLabRow(name string) (labRow, bool) {
	qemu, _, _ := collectQEMURows()
	for _, r := range qemu {
		if r.name == name {
			return r, true
		}
	}
	for _, r := range collectKindRows() {
		if r.name == name {
			return enrichKindHealth([]labRow{r})[0], true
		}
	}
	return labRow{}, false
}

// resolveStatusLab picks the lab: an explicit name, else the lab config
// from -c/--file/--git, else the only astrona lab on the machine.
func resolveStatusLab(labArg string, flags *rootFlags) (string, *config.LabConfig, func(), error) {
	noop := func() {}
	cfg, _, cleanup, err := LoadLabForCommand(flags)
	if err != nil {
		cfg, cleanup = nil, noop
	}

	switch {
	case labArg != "":
		name := config.NormalizeClusterName(labArg)
		if cfg != nil && config.NormalizeClusterName(cfg.Metadata.Name) != name {
			cleanup()
			cfg, cleanup = nil, noop // config is for another lab
		}
		return name, cfg, cleanup, nil
	case cfg != nil:
		return config.NormalizeClusterName(cfg.Metadata.Name), cfg, cleanup, nil
	}

	qemu, _, _ := collectQEMURows()
	var names []string
	for _, r := range append(qemu, collectKindRows()...) {
		if !strings.HasPrefix(r.name, "astro-test-") {
			names = append(names, r.name)
		}
	}
	switch len(names) {
	case 0:
		return "", nil, noop, fmt.Errorf("no astrona lab is running — start one with `astrona run`")
	case 1:
		return names[0], nil, noop, nil
	default:
		return "", nil, noop, fmt.Errorf("several labs are running — pick one: %s", strings.Join(names, ", "))
	}
}

func newStatusCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:               "status [lab-name]",
		Short:             "One-screen overview of a lab: health, access, exam clock, last result",
		ValidArgsFunction: labCompletion(nil),
		Long: "Show where you are with a lab: cluster health, how to connect, port forwards, the " +
			"exam clock (for timed labs), your last submission and score, and what to do next.\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only running lab.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, cfg, cleanup, err := resolveStatusLab(firstArg(args), flags)
			if err != nil {
				return err
			}
			defer cleanup()

			row, ok := findLabRow(name)
			if !ok {
				return fmt.Errorf("lab '%s' isn't running — `astrona run` starts it, `astrona list` shows what is running", name)
			}

			st := labStatus{row: row, cfg: cfg, now: time.Now()}
			if row.runtime == "kind" {
				st.forwards, _ = portforward.List(name)
			}
			st.exam, _ = exam.Load(name)
			st.attempts, _ = proctor.LoadAttempts(name)
			st.links, _ = labLinks(name)
			st.linkRows = map[string]labRow{}
			for _, l := range st.links {
				if r, ok := findLabRow(l.Cluster); ok {
					st.linkRows[l.Cluster] = r
				}
			}
			printLabStatus(os.Stdout, st)
			return nil
		},
	}
}

func printLabStatus(w io.Writer, st labStatus) {
	r := st.row
	fmt.Fprintf(w, "Lab %s (%s)\n\n", r.name, r.runtime)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	field := func(label, value string) { fmt.Fprintf(tw, "  %s\t%s\n", label, value) }

	health := r.status
	if r.version != "" {
		health += " · Kubernetes " + r.version
	}
	if r.uptime != "" && r.uptime != "-" {
		health += " · up " + r.uptime
	}
	field("Cluster", health)

	if r.runtime == "kind" {
		field("Connect", "astrona shell "+r.name)
	} else {
		field("Connect", "astrona ssh "+r.name)
	}

	for i, f := range st.forwards {
		label := ""
		if i == 0 {
			label = "Forwards"
		}
		pf := f.Spec.Forward.Normalized()
		field(label, fmt.Sprintf("%s %s %s", pf.Name, f.Effective(), portforward.LocalURL(pf)))
	}

	for i, l := range st.links {
		label := ""
		if i == 0 {
			label = "Linked"
		}
		state := "not running"
		if r, ok := st.linkRows[l.Cluster]; ok {
			state = r.status
		}
		line := fmt.Sprintf("%s → %s (%s) · %s", l.Name, l.Cluster, state, l.Hostname())
		if !l.WAN.IsZero() {
			line += " · wan " + describeWAN(l.WAN)
		}
		field(label, line)
	}

	if st.exam != nil {
		field("Exam", st.exam.Summary(st.now))
	}

	if n := len(st.attempts); n > 0 {
		a := st.attempts[n-1]
		result := "FAIL"
		if a.Pass {
			result = "PASS"
		}
		pct := 100.0
		if a.Max > 0 {
			pct = 100 * float64(a.Earned) / float64(a.Max)
		}
		field("Last submit", fmt.Sprintf("%d/%d points (%.0f%%) %s · %s ago · attempt #%d",
			a.Earned, a.Max, pct, result, exam.Round(st.now.Sub(a.Time)), n))
	} else {
		field("Last submit", "none yet")
	}
	tw.Flush()

	fmt.Fprintf(w, "\nNext: %s\n", nextStep(st))
}

// nextStep suggests the one most useful command for where the student is.
func nextStep(st labStatus) string {
	if st.cfg != nil && st.cfg.Metadata.Docs.ExamQuestion != "" && len(st.attempts) == 0 {
		return "read the task — astrona docs question"
	}
	if st.exam != nil && st.exam.Over(st.now) {
		return "time is up — astrona submit to record your result, astrona reset to try again"
	}
	if n := len(st.attempts); n > 0 && st.attempts[n-1].Pass {
		return "passed — astrona reset to practice again, astrona destroy when done"
	}
	if strings.HasPrefix(st.row.status, "Stopped") {
		return "astrona start " + st.row.name
	}
	return "astrona submit to grade your work"
}
