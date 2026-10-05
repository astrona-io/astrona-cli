package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"astrona/internal/exam"
	"astrona/internal/proctor"

	"github.com/spf13/cobra"
)

func newProgressCmd() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "progress",
		Short: "Your results across every lab you've submitted",
		Long: "Summarize every lab you've submitted on this machine, from the attempt history " +
			"`astrona submit` records: attempts, best score, whether and on which attempt you " +
			"passed, the fastest passing time for exam labs, and when you last worked on it.\n\n" +
			"-o json prints the same for export or scripts.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if output != "" && output != "json" {
				return fmt.Errorf("unsupported --output '%s' (only json)", output)
			}
			labs, err := proctor.HistoryLabs()
			if err != nil {
				return err
			}
			var all []proctor.LabProgress
			for _, lab := range labs {
				attempts, err := proctor.LoadAttempts(lab)
				if err != nil {
					return err
				}
				if len(attempts) > 0 {
					all = append(all, proctor.SummarizeProgress(lab, attempts))
				}
			}
			if output == "json" {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				if all == nil {
					all = []proctor.LabProgress{}
				}
				return enc.Encode(all)
			}
			printProgressTable(os.Stdout, all, time.Now())
			return nil
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "Output format: json")
	return cmd
}

func printProgressTable(w io.Writer, all []proctor.LabProgress, now time.Time) {
	if len(all) == 0 {
		fmt.Fprintln(w, "No results yet — `astrona submit` grades a lab and records your attempt.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 4, 3, ' ', 0)
	fmt.Fprintln(tw, "LAB\tRESULT\tBEST\tATTEMPTS\tFASTEST PASS\tLAST")
	passed := 0
	for _, p := range all {
		result := "not yet"
		if p.Passed {
			passed++
			result = fmt.Sprintf("passed (#%d)", p.PassedAt)
		}
		fastest := "-"
		if p.FastestPassSeconds > 0 {
			fastest = exam.Round(time.Duration(p.FastestPassSeconds) * time.Second)
		}
		fmt.Fprintf(tw, "%s\t%s\t%d/%d (%.0f%%)\t%d\t%s\t%s\n",
			strings.TrimPrefix(p.Lab, "astro-"), result, p.BestEarned, p.BestMax, p.BestPercent(),
			p.Attempts, fastest, humanAgo(now.Sub(p.LastAttempt)))
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%d of %d lab(s) passed.\n", passed, len(all))
}

// humanAgo is a coarse "how long ago": just now, 12m ago, 5h ago, 3d ago.
func humanAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
