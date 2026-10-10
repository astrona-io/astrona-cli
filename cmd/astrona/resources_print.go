package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"astrona/internal/config"
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
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", sanitizeLine(r.Name), sanitizeLine(desc), ui.Paint(w, sanitizeLine(how), ui.Dim))
	}
	tw.Flush()
}

// resourceRiskLines are the trust prompt's lines about the lab's resources:
// they're its code too, but nothing runs until the student asks.
func resourceRiskLines(cfg *config.LabConfig, baseDir string) []string {
	list, err := resources.Collect(cfg, baseDir)
	if err != nil {
		return []string{sanitizeLine("⚠ its resources can't be read: " + err.Error())}
	}
	if len(list) == 0 {
		return nil
	}
	where := "on this machine"
	if cfg.Runtime.Type == "qemu" {
		where = "inside the lab VM"
	}
	parts := make([]string, len(list))
	for i, r := range list {
		how := r.Command()
		if how == "" {
			how = "file, never run"
		}
		parts[i] = r.Name + " (" + how + ")"
	}
	return []string{sanitizeLine(fmt.Sprintf("ships %d resource(s), copied to ~/.astrona/resources — each runs %s only when you ask (astrona res run): %s",
		len(list), where, strings.Join(parts, ", ")))}
}
