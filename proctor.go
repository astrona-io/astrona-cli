package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CheckResult is the outcome of a single check: did it pass, what did the
// underlying command say, and how long it took — Duration feeds both the
// pytest-style summary line and the JUnit XML report (junit.go).
type CheckResult struct {
	Name     string
	Pass     bool
	Message  string
	Duration time.Duration
}

// Proctor is the sole authority that grades a lab. Student-facing
// `astrona submit` and lab-developer `astrona test` both submit to
// a Proctor instead of running checks themselves — the same way a real exam
// is graded by a proctor, not by the person taking it. Today the Proctor
// still runs on the student's own machine (there is no remote grading
// service yet), but keeping it as one component with a single entry point
// (Grade) means no other command reads validation.checks/validation.script
// directly, and it gives a real remote Proctor service a clean seam to
// slot into later without changing how lab/dev commands call it.
type Proctor struct {
	baseDir     string
	kubeContext string
	executor    ScriptExecutor
}

// NewProctor builds a Proctor scoped to a single lab run: baseDir resolves
// relative script paths, kubeContext pins every kubectl call to the right
// cluster, executor runs the validation script (bash on the host for kind,
// SSH into the VM for qemu).
func NewProctor(baseDir, kubeContext string, executor ScriptExecutor) *Proctor {
	return &Proctor{baseDir: baseDir, kubeContext: kubeContext, executor: executor}
}

// Grade runs the lab's declarative checks and optional script, printing
// pytest/robot-style per-case PASS/FAIL lines followed by a summary line,
// and returns every case's result (for a JUnit report, see junit.go)
// alongside the Proctor's overall verdict.
func (p *Proctor) Grade(config *LabConfig) ([]CheckResult, bool, error) {
	start := time.Now()

	results, err := p.runChecks(config.Validation.Checks)
	if err != nil {
		return nil, false, fmt.Errorf("Proctor checks failed to run: %w", err)
	}

	if config.Validation.Script != nil {
		scriptStart := time.Now()
		scriptPass, err := p.runScript(config.Validation.Script)
		if err != nil {
			return nil, false, fmt.Errorf("Proctor script failed to run: %w", err)
		}

		results = append(results, CheckResult{
			Name:     "validation script",
			Pass:     scriptPass,
			Duration: time.Since(scriptStart),
		})
	}

	pass := true
	passed := 0

	for _, r := range results {
		status := "PASS"
		if !r.Pass {
			status = "FAIL"
			pass = false
		} else {
			passed++
		}

		fmt.Printf("  %-4s  %s (%s)\n", status, r.Name, formatDuration(r.Duration))
		if r.Message != "" {
			fmt.Printf("        %s\n", r.Message)
		}
	}

	failed := len(results) - passed
	fmt.Printf("\n%d passed, %d failed in %s\n", passed, failed, formatDuration(time.Since(start)))

	return results, pass, nil
}

// formatDuration renders a duration the way pytest reports timings:
// fractional seconds, e.g. "0.42s".
func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// runChecks runs each declarative check against the cluster. It does not
// stop at the first failure — it collects every result so Grade can report
// everything at once.
func (p *Proctor) runChecks(checks []ValidationCheck) ([]CheckResult, error) {
	if len(checks) == 0 {
		return nil, nil
	}

	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return nil, fmt.Errorf("kubectl not found in PATH: %w", err)
	}

	results := make([]CheckResult, 0, len(checks))

	for _, c := range checks {
		result := CheckResult{Name: c.Name}
		checkStart := time.Now()

		switch strings.ToLower(c.Type) {
		case "resourceexists":
			args := append([]string{"--context", p.kubeContext, "get"}, strings.Fields(c.Resource)...)
			out, err := exec.Command(kubectlPath, args...).CombinedOutput()
			result.Pass = err == nil
			result.Message = strings.TrimSpace(string(out))
		case "podready":
			args := append([]string{"--context", p.kubeContext, "wait", "--for=condition=Ready", "--timeout=60s"}, strings.Fields(c.Resource)...)
			out, err := exec.Command(kubectlPath, args...).CombinedOutput()
			result.Pass = err == nil
			result.Message = strings.TrimSpace(string(out))
		case "command":
			parts := strings.Fields(c.Command)
			if len(parts) == 0 {
				result.Pass = false
				result.Message = "empty command"
				break
			}

			out, err := exec.Command(parts[0], parts[1:]...).CombinedOutput()
			trimmed := strings.TrimSpace(string(out))

			if c.Expect != "" {
				result.Pass = err == nil && trimmed == c.Expect
			} else {
				result.Pass = err == nil
			}
			result.Message = trimmed
		default:
			result.Pass = false
			result.Message = fmt.Sprintf("unsupported check type '%s'", c.Type)
		}

		result.Duration = time.Since(checkStart)
		results = append(results, result)
	}

	return results, nil
}

// runScript runs the optional custom validation script. Exit code 0 is a
// pass, non-zero is a fail — a failing lab is a normal outcome, not a Go
// error, so only a real execution problem (script missing, bash not found)
// is returned as an error.
func (p *Proctor) runScript(script *ResourceItem) (bool, error) {
	if script == nil || script.Source == "" {
		return true, nil
	}

	scriptPath := script.Source
	cleanup := func() {}

	switch strings.ToLower(script.Type) {
	case "url":
		tmpPath, clean, err := downloadToTemp(script.Source, "astrona-validate-*.sh", maxScriptDownloadBytes)
		if err != nil {
			return false, fmt.Errorf("failed to download validation script from %s: %w", script.Source, err)
		}
		scriptPath = tmpPath
		cleanup = clean
	case "file":
		resolved, err := joinWithinBaseDir(p.baseDir, scriptPath)
		if err != nil {
			return false, fmt.Errorf("failed to resolve validation script path: %w", err)
		}
		scriptPath = resolved
		if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
			return false, fmt.Errorf("validation script does not exist: %s", scriptPath)
		}
	default:
		return false, fmt.Errorf("unsupported type '%s' for validation script (must be 'file' or 'url')", script.Type)
	}
	defer cleanup()

	if err := p.executor.RunScript(scriptPath); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return false, nil
		}
		return false, fmt.Errorf("failed to run validation script: %w", err)
	}

	return true, nil
}
