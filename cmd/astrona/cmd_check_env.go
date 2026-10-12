package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/ui"
)

// checkStatus is one environment check's verdict: ok, a warning (works,
// but likely to cause trouble), or a failure (astrona/kind won't work).
type checkStatus int

const (
	checkOK checkStatus = iota
	checkWarn
	checkFail
)

type checkResult struct {
	status checkStatus
	name   string
	detail string
	hint   string // how to fix; shown for warn/fail
}

const (
	mib int64 = 1 << 20
	gib int64 = 1 << 30
)

func inGiB(b int64) float64 { return float64(b) / float64(gib) }

// Engine memory thresholds: kind's own single-node cluster needs ~1-2 GiB;
// below 4 GiB a lab with workers or addons starts getting evicted/OOM.
const (
	minEngineMemFail = 2 * gib
	minEngineMemWarn = 4 * gib
	// labMemoryHeadroom: warn when a lab is estimated to use more than
	// this share of the engine's memory.
	labMemoryHeadroom = 0.75
)

// engineInfo is what `docker info` / `podman info` tell us about the engine
// (for Docker Desktop / podman machine: the VM kind actually runs in).
type engineInfo struct {
	MemBytes int64
	CPUs     int
	Cgroup   string // "1" or "2"
	Rootless bool
}

// engineInfoFormat is the Go template each engine's `info --format` takes.
func engineInfoFormat(engine string) string {
	if engine == "podman" {
		return "{{.Host.MemTotal}}|{{.Host.CPUs}}|{{.Host.CgroupsVersion}}|{{.Host.Security.Rootless}}"
	}
	return "{{.MemTotal}}|{{.NCPU}}|{{.CgroupVersion}}|{{.SecurityOptions}}"
}

// parseEngineInfo parses the engineInfoFormat output.
func parseEngineInfo(engine, out string) (engineInfo, error) {
	parts := strings.Split(strings.TrimSpace(out), "|")
	if len(parts) != 4 {
		return engineInfo{}, fmt.Errorf("unexpected %s info output %q", engine, out)
	}
	mem, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return engineInfo{}, fmt.Errorf("parse memory %q: %w", parts[0], err)
	}
	cpus, err := strconv.Atoi(parts[1])
	if err != nil {
		return engineInfo{}, fmt.Errorf("parse CPUs %q: %w", parts[1], err)
	}
	info := engineInfo{MemBytes: mem, CPUs: cpus, Cgroup: strings.TrimPrefix(parts[2], "v")}
	if engine == "podman" {
		info.Rootless = parts[3] == "true"
	} else {
		info.Rootless = strings.Contains(parts[3], "rootless")
	}
	return info, nil
}

func engineMemoryHint(engine string) string {
	if engine == "podman" && goruntime.GOOS != "linux" {
		return "podman machine stop && podman machine set --memory 8192 --cpus 4 && podman machine start"
	}
	if engine == "docker" && goruntime.GOOS != "linux" {
		return "Docker Desktop → Settings → Resources: give it at least 8 GB memory and 4 CPUs"
	}
	return "free memory on this machine, or run fewer labs at once"
}

// evaluateEngine turns engine facts into results.
func evaluateEngine(engine string, info engineInfo) []checkResult {
	var res []checkResult

	mem := checkResult{name: engine + " memory", detail: fmt.Sprintf("%.1f GiB", inGiB(info.MemBytes))}
	switch {
	case info.MemBytes < minEngineMemFail:
		mem.status, mem.hint = checkFail, "kind needs at least 2 GiB — "+engineMemoryHint(engine)
	case info.MemBytes < minEngineMemWarn:
		mem.status, mem.hint = checkWarn, "labs with workers or addons may get OOM-killed below 4 GiB — "+engineMemoryHint(engine)
	}
	res = append(res, mem)

	cpu := checkResult{name: engine + " CPUs", detail: strconv.Itoa(info.CPUs)}
	if info.CPUs < 2 {
		cpu.status, cpu.hint = checkWarn, "control-plane components start slowly with 1 CPU — "+engineMemoryHint(engine)
	}
	res = append(res, cpu)

	if info.Rootless {
		cg := checkResult{name: engine + " rootless cgroups", detail: "rootless, cgroup v" + info.Cgroup}
		if info.Cgroup != "2" {
			cg.status, cg.hint = checkFail, "kind with a rootless engine requires cgroup v2 — https://kind.sigs.k8s.io/docs/user/rootless/"
		}
		res = append(res, cg)
	}
	return res
}

// checkEngine runs `<engine> info` to verify connectivity and resources.
func checkEngine() ([]checkResult, *engineInfo) {
	engine, err := cluster.DetectContainerEngine()
	if err != nil {
		return []checkResult{{status: checkFail, name: "container engine", detail: err.Error(), hint: "check your Docker or Podman connection"}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, engine.Path, "info", "--format", engineInfoFormat(engine.Name)).CombinedOutput()
	if err != nil {
		hint := "start the Docker daemon / Docker Desktop"
		if engine.Name == "podman" {
			hint = "podman machine start (macOS/Windows), or check `podman info`"
		}
		return []checkResult{{status: checkFail, name: engine.Name + " reachable", detail: firstLineOf(string(out)), hint: hint}}, nil
	}
	info, err := parseEngineInfo(engine.Name, string(out))
	if err != nil {
		return []checkResult{{status: checkWarn, name: engine.Name + " info", detail: err.Error()}}, nil
	}
	res := []checkResult{{name: engine.Name + " reachable", detail: engine.Path}}
	res = append(res, evaluateEngine(engine.Name, info)...)
	res = append(res, checkKindKeyringQuota(engine)...)
	return res, &info
}

// Read through an existing node so macOS checks the engine's Linux VM,
// not unrelated host sysctls. No helper container or privileged exec needed.
func checkKindKeyringQuota(engine cluster.ContainerEngine) []checkResult {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, engine.Path, "ps", "--filter", "label=io.x-k8s.kind.role=control-plane", "--format", "{{.Names}}").Output()
	if err != nil || len(strings.Fields(string(out))) == 0 {
		return nil
	}
	node := strings.Fields(string(out))[0]
	out, err = exec.CommandContext(ctx, engine.Path, "exec", "--", node, "cat", "/proc/key-users").Output()
	if err != nil {
		return []checkResult{{status: checkWarn, name: "Kind keyring quota", detail: "could not read kernel key quota from " + node, hint: "if pod startup reports 'session key: disk quota exceeded', inspect kernel.keys limits in the engine's Linux VM/host"}}
	}
	return evaluateKeyringQuota(string(out))
}

func evaluateKeyringQuota(out string) []checkResult {
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Fields(line)
		// UID 0 as seen by the node can be an unprivileged VM UID under
		// rootless Podman: use the reported quota, not root_maxkeys.
		if len(parts) != 5 || parts[0] != "0:" {
			continue
		}
		used, keyMax, ok := strings.Cut(parts[3], "/")
		bytesUsed, bytesMax, bytesOK := strings.Cut(parts[4], "/")
		if !ok || !bytesOK {
			continue
		}
		limit, err := strconv.Atoi(keyMax)
		usage, usageErr := strconv.Atoi(used)
		byteLimit, byteErr := strconv.Atoi(bytesMax)
		byteUsage, byteUsageErr := strconv.Atoi(bytesUsed)
		if err != nil || usageErr != nil || byteErr != nil || byteUsageErr != nil || limit <= 0 || byteLimit <= 0 {
			continue
		}
		r := checkResult{name: "Kind keyring quota", detail: used + "/" + keyMax + " keys, " + bytesUsed + "/" + bytesMax + " bytes (shared across clusters)"}
		if limit < 20000 || byteLimit < 500000 || float64(usage)/float64(limit) >= 0.8 || float64(byteUsage)/float64(byteLimit) >= 0.8 {
			r.status = checkWarn
			r.hint = "multiple Kind clusters can exhaust this quota and prevent CoreDNS pods from starting; inspect kernel.keys.maxkeys/maxbytes inside the engine's Linux VM/host — https://github.com/kubernetes-sigs/kind/issues/3806"
		}
		return []checkResult{r}
	}
	return nil
}

// Minimums kind's known-issues page recommends for multi-node clusters;
// below them kubelet/kube-proxy fail with "too many open files".
const (
	minInotifyWatches   = 524288
	minInotifyInstances = 512
)

// checkInotify reads the Linux inotify limits under procRoot (normally
// /proc). Only meaningful on a Linux host — on macOS/Windows the nodes run
// in Docker Desktop's/podman machine's own VM.
func checkInotify(procRoot string) []checkResult {
	limits := []struct {
		file string
		min  int
	}{
		{"sys/fs/inotify/max_user_watches", minInotifyWatches},
		{"sys/fs/inotify/max_user_instances", minInotifyInstances},
	}
	var res []checkResult
	for _, l := range limits {
		name := "fs.inotify." + filepath.Base(l.file)
		data, err := os.ReadFile(filepath.Join(procRoot, l.file))
		if err != nil {
			res = append(res, checkResult{status: checkWarn, name: name, detail: "could not read: " + err.Error()})
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			res = append(res, checkResult{status: checkWarn, name: name, detail: "unparseable value " + strings.TrimSpace(string(data))})
			continue
		}
		r := checkResult{name: name, detail: strconv.Itoa(v)}
		if v < l.min {
			r.status = checkWarn
			r.hint = fmt.Sprintf("multi-node kind clusters fail with 'too many open files' below %d — sudo sysctl %s=%d (persist in /etc/sysctl.d/)", l.min, name, l.min)
		}
		res = append(res, r)
	}
	return res
}

// estimateLabMemory is a rough working-set estimate for a kind lab, from
// observed idle footprints: a control plane (etcd, apiserver, …) dominates,
// each extra node and addon adds on top. Good enough to warn "this won't
// fit", not a precise number.
func estimateLabMemory(k *config.KindConfig) int64 {
	mem := 1200 * mib
	mem += int64(k.ControlPlaneCount()-1) * 900 * mib
	mem += int64(k.WorkerCount()) * 500 * mib
	if k != nil {
		a := k.Addons
		if a.CNI != "" {
			mem += 400 * mib
		}
		if a.CertManager {
			mem += 200 * mib
		}
		if a.MetricsServer {
			mem += 100 * mib
		}
		if a.GatewayAPI != "" {
			mem += 400 * mib
		}
	}
	return mem
}

// evaluateLabMemory compares this lab (plus the linked clusters it would
// create, and labs already running at a default-lab estimate each) with the
// engine's memory.
func evaluateLabMemory(k *config.KindConfig, linked []*config.KindConfig, othersRunning int, engineMem int64) checkResult {
	return evaluateLabMemoryWithOthers(k, linked, kindMemory{clusters: othersRunning, bytes: int64(othersRunning) * estimateLabMemory(nil)}, engineMem)
}

type kindMemory struct {
	clusters int
	bytes    int64
}

func evaluateLabMemoryWithOthers(k *config.KindConfig, linked []*config.KindConfig, others kindMemory, engineMem int64) checkResult {
	lab := estimateLabMemory(k)
	var links int64
	for _, lk := range linked {
		links += estimateLabMemory(lk)
	}
	total := lab + links + others.bytes

	detail := fmt.Sprintf("~%.1f GiB for this lab", inGiB(lab))
	if len(linked) > 0 {
		detail += fmt.Sprintf(" + ~%.1f GiB for %d linked cluster(s)", inGiB(links), len(linked))
	}
	if others.clusters > 0 {
		detail += fmt.Sprintf(" + ~%.1f GiB for %d running Kind cluster(s)", inGiB(others.bytes), others.clusters)
	}
	detail += fmt.Sprintf(" of %.1f GiB", inGiB(engineMem))

	r := checkResult{name: "lab memory estimate", detail: detail}
	if float64(total) > labMemoryHeadroom*float64(engineMem) {
		r.status = checkWarn
		r.hint = "likely too tight — stop other labs (`astrona list`, `astrona destroy <lab>`), use fewer workers/addons, or give the engine more memory"
	}
	return r
}

// Include every running Kind node, even clusters created outside Astrona.
// Stopped containers consume no working memory. Existing addons are unknown;
// this remains a rough estimate, not a measurement of free RAM.
func otherKindMemory(cfg *config.LabConfig, name string) (kindMemory, error) {
	engine, err := cluster.DetectContainerEngine()
	if err != nil {
		return kindMemory{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, engine.Path, "ps", "--filter", "label=io.x-k8s.kind.cluster", "--format",
		`{{.Label "io.x-k8s.kind.cluster"}}|{{.Label "io.x-k8s.kind.role"}}`).Output()
	if err != nil {
		return kindMemory{}, fmt.Errorf("list Kind nodes: %w", err)
	}
	exclude := map[string]bool{name: true}
	for _, c := range cfg.KindClusters() {
		exclude[config.LinkedClusterName(name, c.Name)] = true
	}
	return parseOtherKindMemory(string(out), exclude), nil
}

func parseOtherKindMemory(out string, exclude map[string]bool) kindMemory {
	nodes := map[string]*config.KindConfig{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		name, role, ok := strings.Cut(strings.TrimSpace(line), "|")
		if !ok || name == "" || exclude[name] || (role != "control-plane" && role != "worker") {
			continue
		}
		if nodes[name] == nil {
			nodes[name] = &config.KindConfig{}
		}
		if role == "control-plane" {
			nodes[name].Nodes.ControlPlanes++
		} else {
			nodes[name].Nodes.Workers++
		}
	}
	res := kindMemory{clusters: len(nodes)}
	for _, k := range nodes {
		res.bytes += estimateLabMemory(k)
	}
	return res
}

// labHostPorts lists the host ports a lab binds on 127.0.0.1, with what
// uses each.
func labHostPorts(rt config.RuntimeConfig) map[int]string {
	ports := map[int]string{}
	for _, pf := range rt.PortForwards {
		ports[pf.HostPort] = "port forward '" + pf.Name + "'"
	}
	if rt.Kind != nil && rt.Kind.Addons.GatewayAPI != "" {
		p := rt.Kind.Addons.EffectiveGatewayPorts()
		ports[p.HTTP] = "gateway http"
		ports[p.HTTPS] = "gateway https"
	}
	return ports
}

func checkHostPorts(ports map[int]string) []checkResult {
	var res []checkResult
	for _, port := range sortedPorts(ports) {
		r := checkResult{name: fmt.Sprintf("host port %d", port), detail: ports[port] + ", free"}
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			r.status = checkFail
			r.detail = ports[port] + ", in use"
			r.hint = fmt.Sprintf("find the user with `lsof -nP -iTCP:%d -sTCP:LISTEN`, or change the port in the lab config", port)
		} else {
			l.Close()
		}
		res = append(res, r)
	}
	return res
}

func sortedPorts(ports map[int]string) []int {
	out := make([]int, 0, len(ports))
	for p := range ports {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}

// checkLab runs the lab-specific checks for cfg. engine may be nil (engine
// unreachable — memory can't be judged).
func checkLab(cfg *config.LabConfig, baseDir string, engine *engineInfo) []checkResult {
	// Same config checks as `astrona validate`, typos included.
	res := labConfigResults(cfg, baseDir)
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return res
	}

	name := config.NormalizeClusterName(cfg.Metadata.Name)
	running := kindClusterExists(name)

	// Linked clusters `astrona run` would create count too. They never
	// bind host ports.
	var linked []*config.KindConfig
	if !running {
		for _, l := range cfg.KindClusters() {
			linked = append(linked, l.Cluster())
		}
	}
	ports := labHostPorts(cfg.Runtime)

	if engine != nil {
		others, err := otherKindMemory(cfg, name)
		if err != nil {
			res = append(res, checkResult{status: checkWarn, name: "existing Kind clusters", detail: err.Error(), hint: "memory estimate excludes other clusters"})
		}
		res = append(res, evaluateLabMemoryWithOthers(cfg.Runtime.Kind, linked, others, engine.MemBytes))
	}

	if len(ports) > 0 {
		if running {
			res = append(res, checkResult{name: "host ports", detail: "lab is running — its own forwards/gateway hold them"})
		} else {
			res = append(res, checkHostPorts(ports)...)
		}
	}
	return res
}

// checkRunEnvironment runs before creating the lab or its linked clusters.
// Estimates warn; only definite failures (engine, cgroups, ports) block.
func checkRunEnvironment(cfg *config.LabConfig, baseDir string, rep *ui.Reporter) error {
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return nil
	}
	results, engine := checkEngine()
	results = append(results, checkLab(cfg, baseDir, engine)...)
	if goruntime.GOOS == "linux" {
		results = append(results, checkInotify("/proc")...)
	}
	failed := false
	for _, r := range results {
		if r.status != checkOK {
			rep.Warn("%s: %s — %s", r.name, r.detail, r.hint)
		}
		failed = failed || r.status == checkFail
	}
	if failed {
		return fmt.Errorf("environment checks failed; run `astrona check` for details")
	}
	return nil
}

// printCheckResults prints results and returns how many failed.
func printCheckResults(title string, res []checkResult) int {
	fmt.Printf("\n%s:\n", title)
	failed := 0
	for _, r := range res {
		mark := colorize(ansiGreen, "✓")
		switch r.status {
		case checkWarn:
			mark = colorize(ansiYellow, "⚠")
		case checkFail:
			mark = colorize(ansiRed, "✗")
			failed++
		}
		fmt.Printf("  %s  %-38s %s\n", mark, r.name, r.detail)
		if r.status != checkOK && r.hint != "" {
			fmt.Printf("        %s %s\n", ui.Paint(os.Stdout, "fix:", ui.Yellow, ui.Bold), r.hint)
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

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "no output"
	}
	return s
}
