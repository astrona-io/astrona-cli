package main

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/labops"
	"astrona/internal/lifecycle"
	"astrona/internal/portforward"

	"github.com/spf13/cobra"
)

// labStatus is everything `astrona status` shows about one lab
// (labops.LabStatus).
type labStatus = labops.LabStatus

// Thin wrappers over labops, so call sites keep their names. S15 removes them.
func findLabRow(name string) (labRow, bool) { return labops.FindLabRow(name) }
func nextStep(st labStatus) string          { return labops.NextStep(st) }

// resolveStatusLab picks the lab: an explicit name, else the lab config
// from -c/--file/--git, else the only astrona lab on the machine.
func resolveStatusLab(labArg string, flags *rootFlags) (string, *config.LabConfig, func(), error) {
	cfg, _, cleanup, err := LoadLabForCommand(flags)
	if err != nil {
		cfg, cleanup = nil, nil
	}
	return labops.ResolveStatusLab(labArg, cfg, cleanup)
}

func newStatusCmd(flags *rootFlags) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:               "status [lab-name|catalog-lab]",
		Short:             "One-screen overview of a lab: health, access, exam clock, last result",
		ValidArgsFunction: labCompletion(nil),
		Long: "Show where you are with a lab: cluster health, how to connect, port forwards, the " +
			"exam clock (for timed labs), your last submission and score, and what to do next.\n\n" +
			"With no lab-name, uses the lab config from -c/--file/--git, or the only running lab.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			labName, err := catalogLabArg(firstArg(args), flags)
			if err != nil {
				return err
			}
			name, cfg, cleanup, err := resolveStatusLab(labName, flags)
			if err != nil {
				return err
			}
			defer cleanup()

			res, err := labops.Status(name, cfg, time.Now())
			if err != nil {
				return err
			}
			if output == "json" {
				return printJSON(res)
			}
			printLabStatus(os.Stdout, res.Lab)
			return nil
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}

func printLabStatus(w io.Writer, st labStatus) {
	r := st.Row
	fmt.Fprintf(w, "Lab %s (%s)\n\n", r.Name, r.Runtime)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	field := func(label, value string) { fmt.Fprintf(tw, "  %s\t%s\n", label, value) }

	health := r.Status
	if r.Version != "" {
		health += " · Kubernetes " + r.Version
	}
	if r.Uptime != "" && r.Uptime != "-" {
		health += " · up " + r.Uptime
	}
	field("Cluster", health)

	if r.Runtime == "kind" {
		field("Connect", "astrona shell "+r.Name)
	} else {
		field("Connect", "astrona ssh "+r.Name)
	}

	for i, f := range st.Forwards {
		label := ""
		if i == 0 {
			label = "Forwards"
		}
		pf := f.Spec.Forward.Normalized()
		field(label, fmt.Sprintf("%s %s %s", pf.Name, f.Effective(), portforward.LocalURL(pf)))
	}

	for i, l := range st.Links {
		label := ""
		if i == 0 {
			label = "Linked"
		}
		state := "not running"
		if r, ok := st.LinkRows[l.Cluster]; ok {
			state = r.Status
		}
		line := fmt.Sprintf("%s → %s (%s) · %s", l.Name, l.Cluster, state, l.Hostname())
		if !l.WAN.IsZero() {
			line += " · wan " + lifecycle.DescribeWAN(l.WAN)
		}
		field(label, line)
	}

	if st.Exam != nil {
		field("Exam", st.Exam.Summary(st.Now))
	}

	if n := len(st.Attempts); n > 0 {
		a := st.Attempts[n-1]
		result := "FAIL"
		if a.Pass {
			result = "PASS"
		}
		pct := 100.0
		if a.Max > 0 {
			pct = 100 * float64(a.Earned) / float64(a.Max)
		}
		field("Last submit", fmt.Sprintf("%d/%d points (%.0f%%) %s · %s ago · attempt #%d",
			a.Earned, a.Max, pct, result, exam.Round(st.Now.Sub(a.Time)), n))
	} else {
		field("Last submit", "none yet")
	}
	tw.Flush()

	fmt.Fprintf(w, "\nNext: %s\n", nextStep(st))
}
