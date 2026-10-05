package proctor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/runtime"
	"astrona/internal/scripts"
)

// CheckResult is the outcome of a single check: did it pass, what did the
// underlying command say, and how long it took — Duration feeds both the
// pytest-style summary line and the JUnit XML report (junit.go).
type CheckResult struct {
	Name     string
	Pass     bool
	Message  string
	Duration time.Duration
	Hint     string // shown on failure (unless hints are hidden)
	Points   int    // weight in the score, >= 1
}

// Score is a submission's weighted result.
type Score struct {
	Earned, Max int
}

// Percent is Earned/Max as 0–100 (100 for an empty lab).
func (s Score) Percent() float64 {
	if s.Max == 0 {
		return 100
	}
	return 100 * float64(s.Earned) / float64(s.Max)
}

// ScoreOf sums results' points.
func ScoreOf(results []CheckResult) Score {
	var s Score
	for _, r := range results {
		pts := config.EffectivePoints(r.Points)
		s.Max += pts
		if r.Pass {
			s.Earned += pts
		}
	}
	return s
}

// Passed applies a lab's pass rule: with passPercent set, the score must
// reach it; otherwise every result must pass.
func Passed(results []CheckResult, passPercent int) bool {
	if passPercent > 0 {
		return ScoreOf(results).Percent() >= float64(passPercent)
	}
	for _, r := range results {
		if !r.Pass {
			return false
		}
	}
	return true
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
	baseDir   string
	env       *runtime.LabEnvironment
	hideHints bool
	// scriptOut receives validation scripts' output (os.Stdout unless
	// Quiet). podReadyTimeout bounds podReady checks (60s unless Quiet).
	scriptOut       io.Writer
	podReadyTimeout time.Duration
}

// Quiet makes Evaluate suitable for repeated, live re-grading (`submit
// --watch`): validation script output is discarded and podReady checks
// give up after 2s instead of waiting up to a minute.
func (p *Proctor) Quiet() {
	p.scriptOut = io.Discard
	p.podReadyTimeout = 2 * time.Second
}

// HideHints suppresses failed checks' hints in Grade's report (e.g. exam
// conditions).
func (p *Proctor) HideHints() { p.hideHints = true }

// NewProctor builds a Proctor scoped to a single lab run: baseDir resolves
// relative script paths, env provides both the kubectl context every
// declarative check is pinned to (env.KubeContext) and, via gradeScripts,
// whichever executor(s) run the validation script(s) — bash on the host for
// kind, SSH into a VM for qemu (every VM in turn for a multi-VM lab).
func NewProctor(baseDir string, env *runtime.LabEnvironment) *Proctor {
	return &Proctor{baseDir: baseDir, env: env, scriptOut: os.Stdout, podReadyTimeout: 60 * time.Second}
}

// Grade runs the lab's declarative checks and validation script(s),
// printing pytest/robot-style per-case PASS/FAIL lines followed by a
// summary line, and returns every case's result (for a JUnit report, see
// junit.go) alongside the Proctor's overall verdict.
func (p *Proctor) Grade(cfg *config.LabConfig) ([]CheckResult, bool, error) {
	start := time.Now()

	results, pass, err := p.Evaluate(cfg)
	if err != nil {
		return nil, false, err
	}

	passed := 0
	for _, r := range results {
		status := "PASS"
		if r.Pass {
			passed++
		} else {
			status = "FAIL"
		}

		fmt.Printf("  %-4s  %s (%s)\n", status, r.Name, formatDuration(r.Duration))
		if r.Message != "" {
			fmt.Printf("        %s\n", r.Message)
		}
		if !r.Pass && r.Hint != "" && !p.hideHints {
			fmt.Printf("        hint: %s\n", r.Hint)
		}
	}

	failed := len(results) - passed
	fmt.Printf("\n%d passed, %d failed in %s\n", passed, failed, formatDuration(time.Since(start)))

	score := ScoreOf(results)
	line := fmt.Sprintf("Score: %d/%d points (%.0f%%)", score.Earned, score.Max, score.Percent())
	if pp := cfg.Validation.PassPercent; pp > 0 {
		line += fmt.Sprintf(" — pass mark %d%%", pp)
	}
	fmt.Println(line)

	return results, pass, nil
}

// Evaluate runs every check and validation script and returns the results
// and verdict without printing a report — Grade's engine, also used by
// `submit --watch` to redraw its own live view.
func (p *Proctor) Evaluate(cfg *config.LabConfig) ([]CheckResult, bool, error) {
	results, err := p.runChecks(cfg.Validation.Checks)
	if err != nil {
		return nil, false, fmt.Errorf("Proctor checks failed to run: %w", err)
	}
	scriptResults, err := p.gradeScripts(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("Proctor script failed to run: %w", err)
	}
	results = append(results, scriptResults...)
	return results, Passed(results, cfg.Validation.PassPercent), nil
}

// HintsHidden reports whether hints are suppressed (HideHints).
func (p *Proctor) HintsHidden() bool { return p.hideHints }

// formatDuration renders a duration the way pytest reports timings:
// fractional seconds, e.g. "0.42s".
func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// runChecks runs each declarative check against the cluster. It does not
// stop at the first failure — it collects every result so Grade can report
// everything at once.
func (p *Proctor) runChecks(checks []config.ValidationCheck) ([]CheckResult, error) {
	if len(checks) == 0 {
		return nil, nil
	}

	kubectlPath, err := exec.LookPath("kubectl")
	if err != nil {
		return nil, fmt.Errorf("kubectl not found in PATH: %w", err)
	}

	results := make([]CheckResult, 0, len(checks))

	for _, c := range checks {
		result := CheckResult{Name: c.Name, Hint: c.Hint, Points: config.EffectivePoints(c.Points)}
		checkStart := time.Now()

		switch strings.ToLower(c.Type) {
		case "resourceexists":
			args := append([]string{"--context", p.env.KubeContext, "get"}, strings.Fields(c.Resource)...)
			out, err := exec.Command(kubectlPath, args...).CombinedOutput()
			result.Pass = err == nil
			result.Message = strings.TrimSpace(string(out))
		case "podready":
			args := append([]string{"--context", p.env.KubeContext, "wait", "--for=condition=Ready", fmt.Sprintf("--timeout=%ds", int(p.podReadyTimeout.Seconds()))}, strings.Fields(c.Resource)...)
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

			cmd := exec.Command(parts[0], parts[1:]...)
			// Same KUBECONFIG lab scripts get, so a `kubectl ...` command
			// check grades the lab's cluster, not the user's own context.
			cmd.Env = executor.KubeconfigEnv(p.env.Kubeconfig)
			out, err := cmd.CombinedOutput()
			trimmed := strings.TrimSpace(string(out))

			ok, why := matchOutput(c, trimmed)
			result.Pass = err == nil && ok
			result.Message = joinMessage(trimmed, why)
		case "jsonpath":
			args := append([]string{"--context", p.env.KubeContext, "get"}, strings.Fields(c.Resource)...)
			args = append(args, "-o", "jsonpath="+c.JSONPath)
			out, err := p.kubectl(kubectlPath, args...)
			if err != nil {
				result.Message = out
				break
			}
			ok, why := matchOutput(c, out)
			result.Pass = ok
			if !ok {
				result.Message = fmt.Sprintf("%s is %q — %s", c.JSONPath, out, why)
			}
		case "count":
			args := append([]string{"--context", p.env.KubeContext, "get"}, strings.Fields(c.Resource)...)
			out, err := p.kubectl(kubectlPath, append(args, "-o", "name")...)
			if err != nil {
				result.Message = out
				break
			}
			n := countLines(out)
			result.Pass = withinBounds(n, c.Min, c.Max)
			result.Message = fmt.Sprintf("found %d, want %s", n, describeBounds(c.Min, c.Max))
		case "http":
			result.Pass, result.Message = httpCheck(c)
		default:
			result.Pass = false
			result.Message = fmt.Sprintf("unsupported check type '%s'", c.Type)
		}

		result.Duration = time.Since(checkStart)
		results = append(results, result)
	}

	return results, nil
}

// kubectl runs kubectl with the lab's kubeconfig and returns trimmed
// stdout — or, on failure, trimmed stdout+stderr as the error detail.
func (p *Proctor) kubectl(kubectlPath string, args ...string) (string, error) {
	cmd := exec.Command(kubectlPath, args...)
	cmd.Env = executor.KubeconfigEnv(p.env.Kubeconfig)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(stdout.String() + "\n" + stderr.String()), err
	}
	return strings.TrimSpace(stdout.String()), nil
}

// gradeScripts runs every validation script this lab defines: the shared
// root Validation.Script/Scripts once for a single-environment lab
// (env.Executor != nil), or — for a multi-VM qemu lab — once per VM (in
// config.Runtime.QEMU's order): the shared root ones again first, then that
// VM's own nested QEMUVM.Validation.Script/Scripts. Every multi-VM result's
// name is suffixed "(vmName)" (runValidationBlock) so two VMs running the
// same shared script don't collide in the report — see the design note in
// runValidationBlock for why a shared script's result isn't deduplicated.
func (p *Proctor) gradeScripts(cfg *config.LabConfig) ([]CheckResult, error) {
	if p.env.Executor != nil {
		return p.runValidationBlock(cfg.Validation, p.env.Executor, "")
	}

	var results []CheckResult

	for _, vm := range cfg.Runtime.QEMU {
		executor, err := p.env.ExecutorForVM(vm.Name)
		if err != nil {
			return nil, err
		}

		vmResults, err := p.runValidationBlock(cfg.Validation, executor, vm.Name)
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
func (p *Proctor) runValidationBlock(val config.ValidationConfig, executor executor.ScriptExecutor, vmSuffix string) ([]CheckResult, error) {
	var results []CheckResult

	scriptsList := val.Scripts
	if val.Script != nil {
		scriptsList = append([]config.ResourceItem{*val.Script}, scriptsList...)
	}

	for _, scriptItem := range scriptsList {
		scriptStart := time.Now()
		scriptPass, err := p.runScript(&scriptItem, executor)
		if err != nil {
			return nil, err
		}

		name := scriptItem.Name
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
			Hint:     scriptItem.Hint,
			Points:   config.EffectivePoints(scriptItem.Points),
		})
	}

	return results, nil
}

// runScript runs a single validation script through executor. Exit code 0
// is a pass, non-zero is a fail — a failing lab is a normal outcome, not a
// Go error, so only a real execution problem (script missing, bash not
// found) is returned as an error.
func (p *Proctor) runScript(script *config.ResourceItem, executor executor.ScriptExecutor) (bool, error) {
	if script == nil || script.Source == "" {
		return true, nil
	}

	scriptPath := script.Source
	cleanup := func() {}

	switch strings.ToLower(script.Type) {
	case "url":
		tmpPath, clean, err := config.DownloadToTemp(script.Source, "astrona-validate-*.sh", scripts.MaxScriptDownloadBytes)
		if err != nil {
			return false, fmt.Errorf("failed to download validation script from %s: %w", script.Source, err)
		}
		scriptPath = tmpPath
		cleanup = clean
	case "file":
		resolved, err := config.JoinWithinBaseDir(p.baseDir, scriptPath)
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

	if err := executor.RunScript(scriptPath, p.scriptOut); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, fmt.Errorf("failed to run validation script: %w", err)
	}

	return true, nil
}
