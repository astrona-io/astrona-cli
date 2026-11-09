package preflight

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
)

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
func evaluateLabMemory(k *config.KindConfig, linked []*config.KindConfig, othersRunning int, engineMem int64) Result {
	return evaluateLabMemoryWithOthers(k, linked, kindMemory{clusters: othersRunning, bytes: int64(othersRunning) * estimateLabMemory(nil)}, engineMem)
}

type kindMemory struct {
	clusters int
	bytes    int64
}

func evaluateLabMemoryWithOthers(k *config.KindConfig, linked []*config.KindConfig, others kindMemory, engineMem int64) Result {
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

	r := Result{Name: "lab memory estimate", Detail: detail}
	if float64(total) > labMemoryHeadroom*float64(engineMem) {
		r.Status = Warn
		r.Hint = "likely too tight — stop other labs (`astrona list`, `astrona destroy <lab>`), use fewer workers/addons, or give the engine more memory"
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

func checkHostPorts(ports map[int]string) []Result {
	var res []Result
	for _, port := range sortedPorts(ports) {
		r := Result{Name: fmt.Sprintf("host port %d", port), Detail: ports[port] + ", free"}
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			r.Status = Fail
			r.Detail = ports[port] + ", in use"
			r.Hint = fmt.Sprintf("find the user with `lsof -nP -iTCP:%d -sTCP:LISTEN`, or change the port in the lab config", port)
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

// CheckLab runs the lab-specific checks for cfg: configChecks first (when
// not nil), then, for a kind lab, its memory estimate and host ports.
// engine may be nil (engine unreachable — memory can't be judged).
func CheckLab(cfg *config.LabConfig, baseDir string, engine *EngineInfo, configChecks ConfigChecker) []Result {
	var res []Result
	if configChecks != nil {
		res = configChecks(cfg, baseDir)
	}
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return res
	}

	name := config.NormalizeClusterName(cfg.Metadata.Name)
	running := cluster.Exists(name)

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
			res = append(res, Result{Status: Warn, Name: "existing Kind clusters", Detail: err.Error(), Hint: "memory estimate excludes other clusters"})
		}
		res = append(res, evaluateLabMemoryWithOthers(cfg.Runtime.Kind, linked, others, engine.MemBytes))
	}

	if len(ports) > 0 {
		if running {
			res = append(res, Result{Name: "host ports", Detail: "lab is running — its own forwards/gateway hold them"})
		} else {
			res = append(res, checkHostPorts(ports)...)
		}
	}
	return res
}
