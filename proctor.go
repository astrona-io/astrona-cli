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
	baseDir string
	env     *LabEnvironment
}

// NewProctor builds a Proctor scoped to a single lab run: baseDir resolves
// relative script paths, env provides both the kubectl context every
// declarative check is pinned to (env.KubeContext) and, via gradeScripts,
// whichever executor(s) run the validation script(s) — bash on the host for
// kind, SSH into a VM for qemu (every VM in turn for a multi-VM lab).
func NewProctor(baseDir string, env *LabEnvironment) *Proctor {
	return &Proctor{baseDir: baseDir, env: env}
}

// Grade runs the lab's declarative checks and validation script(s),
// printing pytest/robot-style per-case PASS/FAIL lines followed by a
// summary line, and returns every case's result (for a JUnit report, see
// junit.go) alongside the Proctor's overall verdict.
func (p *Proctor) Grade(config *LabConfig) ([]CheckResult, bool, error) {
	start := time.Now()

	results, err := p.runChecks(config.Validation.Checks)
	if err != nil {
		return nil, false, fmt.Errorf("Proctor checks failed to run: %w", err)
	}

	scriptResults, err := p.gradeScripts(config)
	if err != nil {
		return nil, false, fmt.Errorf("Proctor script failed to run: %w", err)
	}
	results = append(results, scriptResults...)

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
			args := append([]string{"--context", p.env.KubeContext, "get"}, strings.Fields(c.Resource)...)
			out, err := exec.Command(kubectlPath, args...).CombinedOutput()
			result.Pass = err == nil
			result.Message = strings.TrimSpace(string(out))
		case "podready":
			args := append([]string{"--context", p.env.KubeContext, "wait", "--for=condition=Ready", "--timeout=60s"}, strings.Fields(c.Resource)...)
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

// gradeScripts runs every validation script this lab defines: the shared
// root Validation.Script/Scripts once for a single-environment lab
// (env.Executor != nil), or — for a multi-VM qemu lab — once per VM (in
// config.Runtime.QEMU's order): the shared root ones again first, then that
// VM's own nested QEMUVM.Validation.Script/Scripts. Every multi-VM result's
// name is suffixed "(vmName)" (runValidationBlock) so two VMs running the
// same shared script don't collide in the report — see the design note in
// runValidationBlock for why a shared script's result isn't deduplicated.
func (p *Proctor) gradeScripts(config *LabConfig) ([]CheckResult, error) {
	if p.env.Executor != nil {
		return p.runValidationBlock(config.Validation, p.env.Executor, "")
	}

	var results []CheckResult

	for _, vm := range config.Runtime.QEMU {
		executor, err := p.env.executorForVM(vm.Name)
		if err != nil {
			return nil, err
		}

		vmResults, err := p.runValidationBlock(config.Validation, executor, vm.Name)
		if err != nil {
			return nil, err
		}
		results = append(results, vmResults...)

		if vm.Validation != nil {
			ownResults, err := p.runValidationBlock(*vm.Validation, executor, vm.Name)
			if err != nil {
				return nil, err
			}
			results = append(results, ownResults...)
		}
	}

	return results, nil
}

// runValidationBlock runs val's Script (if set) then Scripts (if any)
// through executor, in that order. vmSuffix, non-empty only for a multi-VM
// lab, is appended to each result's name ("name (vmSuffix)") — a shared
// root Validation block genuinely runs once per VM in that case (not a
// single shared result), since it's exercising each VM's own independent
// state, so each run needs its own distinguishable row in the report.
func (p *Proctor) runValidationBlock(val ValidationConfig, executor ScriptExecutor, vmSuffix string) ([]CheckResult, error) {
	var results []CheckResult

	scripts := val.Scripts
	if val.Script != nil {
		scripts = append([]ResourceItem{*val.Script}, scripts...)
	}

	for _, script := range scripts {
		scriptStart := time.Now()
		scriptPass, err := p.runScript(&script, executor)
		if err != nil {
			return nil, err
		}

		name := script.Name
		if name == "" {
			name = "validation script"
		}
		if vmSuffix != "" {
			name = fmt.Sprintf("%s (%s)", name, vmSuffix)
		}

		results = append(results, CheckResult{
			Name:     name,
			Pass:     scriptPass,
			Duration: time.Since(scriptStart),
		})
	}

	return results, nil
}

// runScript runs a single validation script through executor. Exit code 0
// is a pass, non-zero is a fail — a failing lab is a normal outcome, not a
// Go error, so only a real execution problem (script missing, bash not
// found) is returned as an error.
func (p *Proctor) runScript(script *ResourceItem, executor ScriptExecutor) (bool, error) {
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

	if err := executor.RunScript(scriptPath); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return false, nil
		}
		return false, fmt.Errorf("failed to run validation script: %w", err)
	}

	return true, nil
}
