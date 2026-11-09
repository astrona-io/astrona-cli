// Package preflight checks that this machine can run a lab: astrona's
// external dependencies, the container engine's reachability and resources,
// Linux inotify limits, and a lab's estimated memory and host ports. It
// returns results and never prints them; `astrona check`, `doctor` and
// `run` decide how to show them.
package preflight

import (
	"fmt"
	goruntime "runtime"

	"astrona/internal/config"
	"astrona/internal/ui"
)

// Status is one environment check's verdict: ok, a warning (works,
// but likely to cause trouble), or a failure (astrona/kind won't work).
type Status int

const (
	OK Status = iota
	Warn
	Fail
)

// String is the status as -o json spells it: ok, warn or fail.
func (s Status) String() string {
	return map[Status]string{OK: "ok", Warn: "warn", Fail: "fail"}[s]
}

// Result is one ✓/⚠/✗ line of check, doctor and validate.
type Result struct {
	Status Status
	Name   string
	Detail string
	Hint   string // how to fix; shown for warn/fail
}

// ConfigChecker checks a loaded lab config without creating anything
// (what `astrona validate` runs). The lab checks take it as a parameter so
// this package stays below the authoring code that implements it.
type ConfigChecker func(cfg *config.LabConfig, baseDir string) []Result

// RunEnvironment runs before creating the lab or its linked clusters.
// Estimates warn; only definite failures (engine, cgroups, ports) block.
// configChecks may be nil.
func RunEnvironment(cfg *config.LabConfig, baseDir string, rep *ui.Reporter, configChecks ConfigChecker) error {
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return nil
	}
	results, engine := CheckEngine()
	results = append(results, CheckLab(cfg, baseDir, engine, configChecks)...)
	if goruntime.GOOS == "linux" {
		results = append(results, CheckInotify("/proc")...)
	}
	failed := false
	for _, r := range results {
		if r.Status != OK {
			rep.Warn("%s: %s — %s", r.Name, r.Detail, r.Hint)
		}
		failed = failed || r.Status == Fail
	}
	if failed {
		return fmt.Errorf("environment checks failed; run `astrona check` for details")
	}
	return nil
}
