package main

import (
	"fmt"
	"os/exec"
	goruntime "runtime"
	"strings"

	"astrona/internal/config"
	"astrona/internal/preflight"

	"github.com/spf13/cobra"
)

// brewAvailable: Homebrew is installed, so a missing tool's hint can be a
// `brew install` command instead of a web page. Swapped out in tests.
var brewAvailable = func() bool {
	_, err := exec.LookPath("brew")
	return err == nil
}

// depHint is how to install c: the brew command when Homebrew is there and
// has it, else its InstallHint.
func depHint(c preflight.DepCheck) string {
	if c.BrewFormula == "" || !brewAvailable() {
		return c.InstallHint
	}
	if c.BrewFormula == "podman" {
		return "brew install podman && podman machine init && podman machine start — or Docker Desktop"
	}
	return "brew install " + c.BrewFormula
}

// brewInstallAll is one `brew install …` for every missing check Homebrew
// can install, or "" when there's none (or no Homebrew). qemu backs three
// checks but is listed once.
func brewInstallAll(missing []preflight.DepCheck) string {
	if !brewAvailable() {
		return ""
	}
	var formulas []string
	seen := map[string]bool{}
	for _, c := range missing {
		if c.BrewFormula != "" && !seen[c.BrewFormula] {
			seen[c.BrewFormula] = true
			formulas = append(formulas, c.BrewFormula)
		}
	}
	if len(formulas) == 0 {
		return ""
	}
	return "brew install " + strings.Join(formulas, " ")
}

// newCheckCmd builds `astrona check`: verifies every external binary
// astrona shells out to is on PATH, printing a colored ✓/⚠/✗ per
// dependency. A missing required dependency (the default kind runtime's
// own toolchain) is a non-zero exit; a missing optional one (qemu, git) is
// a warning only, since it's only needed for a specific runtime or flag.
func newCheckCmd(flags *rootFlags) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check this machine can run labs (tools, container engine, a lab's memory and ports)",
		Long: "Check that astrona's dependencies are installed, that the container engine is running " +
			"with enough memory/CPUs (and, on Linux, sufficient inotify limits for multi-node kind), " +
			"and — when a lab config is found via -c (default: ./config.yaml) — that the lab's " +
			"estimated memory fits and its host ports are free. A lab that is there but can't be " +
			"loaded (malformed config, broken `astrona use` lab) is a ✗ under Lab.\n\n" +
			"Exits non-zero on any ✗; ⚠ warnings don't fail.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkOutput(output); err != nil {
				return err
			}
			if output == "json" {
				return checkAsJSON(cmd, flags)
			}
			checks := preflight.AstronaDepChecks()

			missingRequired := 0
			printedOptionalHeader := false
			var missingReq, missingOpt []preflight.DepCheck

			fmt.Printf("astrona version: %s\n\n", Version)

			fmt.Println("Required:")
			for _, c := range checks {
				if !c.Required && !printedOptionalHeader {
					fmt.Println("\nOptional (only needed for specific runtimes/flags):")
					printedOptionalHeader = true
				}

				found, detail := c.Find()
				if found {
					fmt.Printf("  %s  %-38s %s\n", colorize(ansiGreen, "✓"), c.Name, detail)
					continue
				}

				if c.Required {
					missingRequired++
					missingReq = append(missingReq, c)
					fmt.Printf("  %s  %-38s %s\n", colorize(ansiRed, "✗"), c.Name, c.Note)
				} else {
					missingOpt = append(missingOpt, c)
					fmt.Printf("  %s  %-38s %s\n", colorize(ansiYellow, "⚠"), c.Name, c.Note)
				}
				fmt.Printf("        install: %s\n", depHint(c))
			}
			if all := brewInstallAll(missingReq); all != "" {
				fmt.Printf("\nInstall what's required: %s\n", all)
			}
			if all := brewInstallAll(missingOpt); all != "" {
				fmt.Printf("\nOptional tools, all at once: %s\n", all)
			}

			envRes, engine := preflight.CheckEngine()
			if goruntime.GOOS == "linux" {
				envRes = append(envRes, preflight.CheckInotify("/proc")...)
			}
			envFailed := printCheckResults("Container engine", envRes)

			explicit := cmd.Flags().Changed("config") || cmd.Flags().Changed("file") || cmd.Flags().Changed("git")
			cfg, baseDir, cleanup, err := loadLabForCheck(flags, explicit)
			defer cleanup()
			if err != nil {
				envFailed += printCheckResults("Lab", []preflight.Result{doctorLabError(err)})
			}
			if cfg != nil {
				envFailed += printCheckResults("Lab "+config.NormalizeClusterName(cfg.Metadata.Name), checkLab(cfg, baseDir, engine))
			}

			fmt.Println()
			if missingRequired > 0 {
				return fmt.Errorf("%d required dependency(ies) missing (see ✗ above) — astrona cannot run until these are installed", missingRequired)
			}
			if envFailed > 0 {
				return fmt.Errorf("%d check(s) failed (see ✗ above)", envFailed)
			}

			fmt.Println("All required checks passed. ⚠ warnings above are worth fixing but don't block astrona.")
			return nil
		},
	}
	addOutputFlag(cmd, &output)
	return cmd
}

// checkAsJSON is `astrona check -o json`: the same checks as one document.
func checkAsJSON(cmd *cobra.Command, flags *rootFlags) error {
	rep := &report{json: true}
	var deps []preflight.Result
	for _, c := range preflight.AstronaDepChecks() {
		found, detail := c.Find()
		switch {
		case found:
			deps = append(deps, preflight.Result{Name: c.Name, Detail: detail})
		case c.Required:
			deps = append(deps, preflight.Result{Status: preflight.Fail, Name: c.Name, Detail: c.Note, Hint: "install: " + depHint(c)})
		default:
			deps = append(deps, preflight.Result{Status: preflight.Warn, Name: c.Name, Detail: c.Note, Hint: "install: " + depHint(c)})
		}
	}
	rep.section("Dependencies", deps)
	envRes, engine := preflight.CheckEngine()
	if goruntime.GOOS == "linux" {
		envRes = append(envRes, preflight.CheckInotify("/proc")...)
	}
	rep.section("Container engine", envRes)
	explicit := cmd.Flags().Changed("config") || cmd.Flags().Changed("file") || cmd.Flags().Changed("git")
	cfg, baseDir, cleanup, err := loadLabForCheck(flags, explicit)
	defer cleanup()
	if err != nil {
		return rep.loadFailed("Lab", err)
	}
	if cfg != nil {
		rep.section("Lab "+config.NormalizeClusterName(cfg.Metadata.Name), checkLab(cfg, baseDir, engine))
	}
	return rep.done(fmt.Errorf("%d check(s) failed", failedCount(rep.results)))
}
