// Package diagnostics collects a debugging bundle from a lab environment —
// for `astrona test` when it fails (before teardown destroys the evidence)
// and for `astrona diagnose` on a running lab.
//
// The bundle is a plain directory (0700): a summary.md with what's wrong at
// a glance, plus raw material to dig into. Deliberately never collected:
// Secrets, ConfigMaps, kubeconfigs — the bundle is meant to be uploaded as
// a CI artifact.
package diagnostics

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/executor"
	"astrona/internal/ui"
)

const (
	// maxProblemPods bounds how many unhealthy pods get a describe + logs.
	maxProblemPods = 25
	logTailLines   = "500"
	maxEventLines  = 25
)

// DefaultDir is ~/.astrona/diagnostics/<lab>-<UTC stamp>.
func DefaultDir(lab string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	return filepath.Join(home, ".astrona", "diagnostics", lab+"-"+time.Now().UTC().Format("20060102T150405Z")), nil
}

// Summary is what got collected, for the caller to print.
type Summary struct {
	Dir      string
	Problems []string // one line per unhealthy pod, e.g. "default/web-1: ImagePullBackOff (…)"
	Warnings int      // number of Warning events
}

// Kind identifies a kind cluster to collect from. Kubeconfig may be "" (lab
// created before kubeconfig isolation) — kubectl then uses the user's own
// kubeconfig, still pinned by --context.
type Kind struct {
	Name        string
	KubeContext string
	Kubeconfig  string
}

// collector runs the commands and writes the bundle. Individual failures
// are recorded in errors.txt instead of aborting — a half-broken cluster is
// exactly when diagnostics matter most.
type collector struct {
	dir     string
	errs    []string
	kubeEnv []string
	kind    Kind
	out     io.Writer
}

func newCollector(dir string, out io.Writer) (*collector, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create diagnostics dir '%s': %w", dir, err)
	}
	return &collector{dir: dir, out: out}, nil
}

func (c *collector) fail(what string, err error) {
	c.errs = append(c.errs, fmt.Sprintf("%s: %s", what, err))
	fmt.Fprintf(c.out, "diagnostics: %s: %s\n", what, err)
}

func (c *collector) write(name string, data []byte) {
	path := filepath.Join(c.dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		c.fail("write "+name, err)
		return
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		c.fail("write "+name, err)
	}
}

// run executes name+args and returns combined output; a failing command's
// output is still returned (it's often the useful part).
func (c *collector) run(env []string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

func (c *collector) kubectl(args ...string) ([]byte, error) {
	return c.run(c.kubeEnv, "kubectl", append([]string{"--context", c.kind.KubeContext}, args...)...)
}

// kubectlTo runs kubectl and saves its output to file, whatever the
// outcome.
func (c *collector) kubectlTo(file string, args ...string) {
	out, err := c.kubectl(args...)
	c.write(file, out)
	if err != nil {
		c.fail("kubectl "+strings.Join(args, " "), err)
	}
}

func (c *collector) finish() {
	if len(c.errs) > 0 {
		c.write("errors.txt", []byte(strings.Join(c.errs, "\n")+"\n"))
	}
}

// CollectKind gathers a kind cluster's bundle into dir.
func CollectKind(k Kind, dir string, rep *ui.Reporter) (Summary, error) {
	t := rep.Step("Collect diagnostics for %q", k.Name)
	c, err := newCollector(dir, t.Output())
	if err != nil {
		return Summary{}, t.Fail(err)
	}
	c.kind = k
	c.kubeEnv = executor.KubeconfigEnv(k.Kubeconfig)
	sum := Summary{Dir: dir}

	c.write("versions.txt", c.versions())

	c.kubectlTo("cluster/nodes.txt", "get", "nodes", "-o", "wide")
	c.kubectlTo("cluster/describe-nodes.txt", "describe", "nodes")
	c.kubectlTo("cluster/pods.txt", "get", "pods", "-A", "-o", "wide")
	c.kubectlTo("cluster/workloads.txt", "get", "deploy,sts,ds,rs,job,svc,ingress,pvc", "-A", "-o", "wide")
	c.kubectlTo("cluster/events.txt", "get", "events", "-A", "--sort-by=.lastTimestamp")
	// Present only if the Gateway API addon (or the lab) installed it.
	if out, err := c.kubectl("get", "gatewayclass,gateway,httproute", "-A", "-o", "wide"); err == nil {
		c.write("cluster/gateway-api.txt", out)
	}

	warnings := c.warningEvents()
	sum.Warnings = len(warnings)

	problems := c.problemPods()
	for i, p := range problems {
		sum.Problems = append(sum.Problems, p.line())
		if i >= maxProblemPods {
			continue
		}
		base := fmt.Sprintf("pods/%s_%s", p.Namespace, p.Name)
		c.kubectlTo(base+".describe.txt", "-n", p.Namespace, "describe", "pod", p.Name)
		for _, ctr := range p.Containers {
			if ctr.NoLogs {
				continue
			}
			c.kubectlTo(fmt.Sprintf("%s_%s.log", base, ctr.Name),
				"-n", p.Namespace, "logs", p.Name, "-c", ctr.Name, "--tail="+logTailLines)
			if ctr.Restarts > 0 {
				c.kubectlTo(fmt.Sprintf("%s_%s.previous.log", base, ctr.Name),
					"-n", p.Namespace, "logs", p.Name, "-c", ctr.Name, "--previous", "--tail="+logTailLines)
			}
		}
	}

	// kind's own export: node journals, kubelet/containerd logs, every
	// pod's log files. Needs the podman provider switch like every other
	// kind call.
	kindEnv := os.Environ()
	if engine, err := cluster.DetectContainerEngine(); err == nil && engine.Name == "podman" {
		kindEnv = append(kindEnv, "KIND_EXPERIMENTAL_PROVIDER=podman")
	}
	if out, err := c.run(kindEnv, "kind", "export", "logs", filepath.Join(dir, "kind-logs"), "--name", k.Name); err != nil {
		c.fail("kind export logs", fmt.Errorf("%w: %s", err, lastLine(out)))
	}

	c.write("summary.md", renderSummary(k.Name, sum, warnings, len(problems) > maxProblemPods))
	c.finish()
	t.Done()
	return sum, nil
}

// QEMUVM identifies one VM whose serial console log to keep.
type QEMUVM struct {
	Name       string // display name ("router", or the lab name for a single-VM lab)
	ConsoleLog string // path to its console.log
}

// CollectQEMU copies each VM's serial console log into dir — the VM state
// dir (and the log with it) is deleted on teardown.
func CollectQEMU(lab string, vms []QEMUVM, dir string, rep *ui.Reporter) (Summary, error) {
	t := rep.Step("Collect diagnostics for %q", lab)
	c, err := newCollector(dir, t.Output())
	if err != nil {
		return Summary{}, t.Fail(err)
	}
	c.write("versions.txt", c.versions())
	for _, vm := range vms {
		data, err := os.ReadFile(vm.ConsoleLog)
		if err != nil {
			c.fail("console log for "+vm.Name, err)
			continue
		}
		c.write(fmt.Sprintf("vms/%s.console.log", vm.Name), data)
	}
	sum := Summary{Dir: dir}
	c.write("summary.md", renderSummary(lab, sum, nil, false))
	c.finish()
	t.Done()
	return sum, nil
}

func (c *collector) versions() []byte {
	var b strings.Builder
	for _, cmd := range [][]string{{"kind", "version"}, {"kubectl", "version", "--client"}, {"docker", "version", "--format", "{{.Server.Version}}"}, {"podman", "version", "--format", "{{.Server.Version}}"}} {
		if _, err := exec.LookPath(cmd[0]); err != nil {
			continue
		}
		out, _ := c.run(nil, cmd[0], cmd[1:]...)
		fmt.Fprintf(&b, "$ %s\n%s\n", strings.Join(cmd, " "), bytes.TrimSpace(out))
	}
	return []byte(b.String())
}

// --- pod health ------------------------------------------------------------

type podList struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Status struct {
			Phase      string `json:"phase"`
			Reason     string `json:"reason"`
			Conditions []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			} `json:"conditions"`
			InitContainerStatuses []containerStatus `json:"initContainerStatuses"`
			ContainerStatuses     []containerStatus `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

type containerStatus struct {
	Name         string `json:"name"`
	Ready        bool   `json:"ready"`
	RestartCount int    `json:"restartCount"`
	State        struct {
		Waiting *struct {
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"waiting"`
		Terminated *struct {
			Reason   string `json:"reason"`
			ExitCode int    `json:"exitCode"`
		} `json:"terminated"`
	} `json:"state"`
}

type problemPod struct {
	Namespace, Name, Reason string
	Containers              []problemContainer
}

type problemContainer struct {
	Name     string
	Restarts int
	// NoLogs: the container never started (image pull, still creating),
	// so `kubectl logs` would only error.
	NoLogs bool
}

// neverStarted are waiting reasons for a container that has no logs yet.
var neverStarted = map[string]bool{
	"ErrImagePull": true, "ImagePullBackOff": true, "InvalidImageName": true,
	"ContainerCreating": true, "PodInitializing": true, "CreateContainerConfigError": true,
}

func (p problemPod) line() string {
	return fmt.Sprintf("%s/%s: %s", p.Namespace, p.Name, p.Reason)
}

func (c *collector) problemPods() []problemPod {
	out, err := c.kubectl("get", "pods", "-A", "-o", "json")
	if err != nil {
		c.fail("list pods", fmt.Errorf("%w: %s", err, lastLine(out)))
		return nil
	}
	var list podList
	if err := json.Unmarshal(out, &list); err != nil {
		c.fail("parse pods", err)
		return nil
	}
	return findProblems(list)
}

// findProblems picks pods that aren't healthy — not Running/Succeeded,
// Running with an unready container, or restarted — with the most telling
// reason kubectl exposes (a container's waiting/terminated reason beats
// the pod phase).
func findProblems(list podList) []problemPod {
	var probs []problemPod
	for _, it := range list.Items {
		st := it.Status
		p := problemPod{Namespace: it.Metadata.Namespace, Name: it.Metadata.Name}
		var reasons []string
		bad := st.Phase != "Running" && st.Phase != "Succeeded"

		for _, cs := range append(append([]containerStatus{}, st.InitContainerStatuses...), st.ContainerStatuses...) {
			p.Containers = append(p.Containers, problemContainer{
				Name:     cs.Name,
				Restarts: cs.RestartCount,
				NoLogs:   cs.RestartCount == 0 && cs.State.Waiting != nil && neverStarted[cs.State.Waiting.Reason],
			})
			switch {
			case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
				r := cs.State.Waiting.Reason
				if m := firstLine(cs.State.Waiting.Message); m != "" {
					r += " (" + m + ")"
				}
				reasons = append(reasons, cs.Name+": "+r)
				bad = true
			case cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0:
				reasons = append(reasons, fmt.Sprintf("%s: %s (exit %d)", cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.ExitCode))
				bad = true
			}
			if cs.RestartCount > 0 {
				reasons = append(reasons, fmt.Sprintf("%s restarted %d×", cs.Name, cs.RestartCount))
				bad = true
			}
			if st.Phase == "Running" && !cs.Ready && cs.State.Waiting == nil {
				reasons = append(reasons, cs.Name+": not ready")
				bad = true
			}
		}
		if !bad {
			continue
		}
		if st.Phase == "Pending" && len(reasons) == 0 {
			for _, cond := range st.Conditions {
				if cond.Type == "PodScheduled" && cond.Status == "False" {
					reasons = append(reasons, "Unschedulable ("+firstLine(cond.Message)+")")
				}
			}
		}
		if len(reasons) == 0 {
			reasons = append(reasons, strings.TrimSpace(st.Phase+" "+st.Reason))
		}
		p.Reason = strings.Join(reasons, "; ")
		probs = append(probs, p)
	}
	sort.Slice(probs, func(i, j int) bool { return probs[i].line() < probs[j].line() })
	return probs
}

// warningEvents returns the newest Warning events as display lines.
func (c *collector) warningEvents() []string {
	out, err := c.kubectl("get", "events", "-A", "--field-selector=type=Warning", "--sort-by=.lastTimestamp",
		"-o", "custom-columns=NAMESPACE:.metadata.namespace,OBJECT:.involvedObject.name,REASON:.reason,MESSAGE:.message", "--no-headers")
	if err != nil {
		c.fail("list warning events", fmt.Errorf("%w: %s", err, lastLine(out)))
		return nil
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "No resources found") {
			lines = append(lines, collapseSpaces.ReplaceAllString(l, " "))
		}
	}
	return lines
}

var collapseSpaces = regexp.MustCompile(`\s{2,}`)

func renderSummary(lab string, sum Summary, warnings []string, truncated bool) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Diagnostics: %s\n\nCollected %s by astrona.\n\n", lab, time.Now().UTC().Format(time.RFC3339))

	fmt.Fprintf(&b, "## Unhealthy pods (%d)\n\n", len(sum.Problems))
	if len(sum.Problems) == 0 {
		b.WriteString("None.\n")
	}
	for _, p := range sum.Problems {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	if truncated {
		fmt.Fprintf(&b, "\n(describe/logs collected for the first %d only)\n", maxProblemPods)
	}

	fmt.Fprintf(&b, "\n## Warning events (%d, newest last)\n\n", len(warnings))
	if len(warnings) == 0 {
		b.WriteString("None.\n")
	}
	if len(warnings) > maxEventLines {
		warnings = warnings[len(warnings)-maxEventLines:]
	}
	for _, w := range warnings {
		fmt.Fprintf(&b, "- %s\n", w)
	}

	b.WriteString(`
## Files

- cluster/ — nodes, pods, workloads, events (kubectl get/describe)
- pods/ — describe + logs (and previous logs after a restart) for each unhealthy pod
- kind-logs/ — ` + "`kind export logs`" + `: node journals, kubelet, containerd, all pod logs
- vms/ — serial console log per VM (qemu labs)
- versions.txt, errors.txt (anything that couldn't be collected)
`)
	return []byte(b.String())
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func lastLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// UnhealthyPods lists k's unhealthy pods ("namespace/name: reason"), the
// same ones a diagnostics bundle describes — without writing a bundle.
func UnhealthyPods(k Kind) ([]string, error) {
	cmd := exec.Command("kubectl", "--context", k.KubeContext, "get", "pods", "-A", "-o", "json", "--request-timeout=10s")
	cmd.Env = executor.KubeconfigEnv(k.Kubeconfig)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	var list podList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse pods: %w", err)
	}
	var lines []string
	for _, p := range findProblems(list) {
		lines = append(lines, p.line())
	}
	return lines, nil
}
