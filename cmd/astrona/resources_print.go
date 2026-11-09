package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/resources"
	"astrona/internal/ui"
)

// keepResources is `astrona destroy --keep-resources`: leave the lab's copy
// of its resources in ~/.astrona/resources for reference.
var keepResources bool

// printResources is the block `astrona run` / `reset` print when the lab has
// resources: where they were copied and what each one is. Nothing in them
// has run.
func printResources(w io.Writer, dir string, list []resources.Resource) {
	if len(list) == 0 {
		return
	}
	fmt.Fprintf(w, "\nResources (%d) — copied to %s, nothing has run:\n", len(list), dir)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range list {
		how := r.Command()
		if how == "" {
			how = "file"
			if r.Dir {
				how = "folder"
			}
		}
		desc := r.Description
		if desc == "" {
			desc = r.File
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", sanitizeLine(r.File), sanitizeLine(desc), ui.Paint(w, sanitizeLine(how), ui.Dim))
	}
	tw.Flush()
	fmt.Fprintln(w, "In the lab terminal: ~/resources · astrona res run <filename> -- <args>")
}

// resourceRiskLines are the trust prompt's lines about the lab's resources
// (labops.ResourceRiskLines).
func resourceRiskLines(cfg *config.LabConfig, baseDir string) []string {
	return labops.ResourceRiskLines(cfg, baseDir)
}
