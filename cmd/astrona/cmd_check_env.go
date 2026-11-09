package main

import (
	"fmt"
	"os"
	"strings"

	"astrona/internal/authoring"
	"astrona/internal/config"
	"astrona/internal/preflight"
	"astrona/internal/ui"
)

// checkLab is preflight.CheckLab with the same config checks as
// `astrona validate`, typos included.
func checkLab(cfg *config.LabConfig, baseDir string, engine *preflight.EngineInfo) []preflight.Result {
	return preflight.CheckLab(cfg, baseDir, engine, authoring.CheckConfig)
}

// printCheckResults prints results and returns how many failed.
func printCheckResults(title string, res []preflight.Result) int {
	fmt.Printf("\n%s:\n", title)
	failed := 0
	for _, r := range res {
		mark := colorize(ansiGreen, "✓")
		switch r.Status {
		case preflight.Warn:
			mark = colorize(ansiYellow, "⚠")
		case preflight.Fail:
			mark = colorize(ansiRed, "✗")
			failed++
		}
		fmt.Printf("  %s  %-38s %s\n", mark, r.Name, r.Detail)
		if r.Status != preflight.OK && r.Hint != "" {
			fmt.Printf("        %s %s\n", ui.Paint(os.Stdout, "fix:", ui.Yellow, ui.Bold), r.Hint)
		}
	}
	return failed
}

// loadLabForCheck loads the lab config for lab-specific checks. With the
// default -c ".", a directory without a config is simply "no lab"; any
// other failure — a malformed ./config.yaml, a broken `astrona use` lab, or
// an explicitly given -c/--file/--git — is an error the caller reports.
func loadLabForCheck(flags *rootFlags, explicit bool) (*config.LabConfig, string, func(), error) {
	cfg, baseDir, cleanup, err := LoadLabForCommand(flags)
	if err == nil {
		if strings.HasPrefix(baseDir, "http://") || strings.HasPrefix(baseDir, "https://") {
			baseDir = ""
		}
		return cfg, baseDir, cleanup, nil
	}
	return nil, "", func() {}, noLabOr(err, explicit || flags.fromCurrent)
}
