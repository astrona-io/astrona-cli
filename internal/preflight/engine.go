package preflight

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"astrona/internal/cluster"
)

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

// EngineInfo is what `docker info` / `podman info` tell us about the engine
// (for Docker Desktop / podman machine: the VM kind actually runs in).
type EngineInfo struct {
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
func parseEngineInfo(engine, out string) (EngineInfo, error) {
	parts := strings.Split(strings.TrimSpace(out), "|")
	if len(parts) != 4 {
		return EngineInfo{}, fmt.Errorf("unexpected %s info output %q", engine, out)
	}
	mem, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return EngineInfo{}, fmt.Errorf("parse memory %q: %w", parts[0], err)
	}
	cpus, err := strconv.Atoi(parts[1])
	if err != nil {
		return EngineInfo{}, fmt.Errorf("parse CPUs %q: %w", parts[1], err)
	}
	info := EngineInfo{MemBytes: mem, CPUs: cpus, Cgroup: strings.TrimPrefix(parts[2], "v")}
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
func evaluateEngine(engine string, info EngineInfo) []Result {
	var res []Result

	mem := Result{Name: engine + " memory", Detail: fmt.Sprintf("%.1f GiB", inGiB(info.MemBytes))}
	switch {
	case info.MemBytes < minEngineMemFail:
		mem.Status, mem.Hint = Fail, "kind needs at least 2 GiB — "+engineMemoryHint(engine)
	case info.MemBytes < minEngineMemWarn:
		mem.Status, mem.Hint = Warn, "labs with workers or addons may get OOM-killed below 4 GiB — "+engineMemoryHint(engine)
	}
	res = append(res, mem)

	cpu := Result{Name: engine + " CPUs", Detail: strconv.Itoa(info.CPUs)}
	if info.CPUs < 2 {
		cpu.Status, cpu.Hint = Warn, "control-plane components start slowly with 1 CPU — "+engineMemoryHint(engine)
	}
	res = append(res, cpu)

	if info.Rootless {
		cg := Result{Name: engine + " rootless cgroups", Detail: "rootless, cgroup v" + info.Cgroup}
		if info.Cgroup != "2" {
			cg.Status, cg.Hint = Fail, "kind with a rootless engine requires cgroup v2 — https://kind.sigs.k8s.io/docs/user/rootless/"
		}
		res = append(res, cg)
	}
	return res
}

// CheckEngine runs `<engine> info` to verify connectivity and resources.
// The first result says whether the engine is reachable. The info is nil
// when it is not (or its output can't be parsed).
func CheckEngine() ([]Result, *EngineInfo) {
	engine, err := cluster.DetectContainerEngine()
	if err != nil {
		return []Result{{Status: Fail, Name: "container engine", Detail: err.Error(), Hint: "check your Docker or Podman connection"}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, engine.Path, "info", "--format", engineInfoFormat(engine.Name)).CombinedOutput()
	if err != nil {
		hint := "start the Docker daemon / Docker Desktop"
		if engine.Name == "podman" {
			hint = "podman machine start (macOS/Windows), or check `podman info`"
		}
		return []Result{{Status: Fail, Name: engine.Name + " reachable", Detail: firstLineOf(string(out)), Hint: hint}}, nil
	}
	info, err := parseEngineInfo(engine.Name, string(out))
	if err != nil {
		return []Result{{Status: Warn, Name: engine.Name + " info", Detail: err.Error()}}, nil
	}
	res := []Result{{Name: engine.Name + " reachable", Detail: engine.Path}}
	res = append(res, evaluateEngine(engine.Name, info)...)
	res = append(res, checkKindKeyringQuota(engine)...)
	res = append(res, checkKindForPodman(engine)...)
	return res, &info
}

// kindForPodman6 is the first kind release that reads Podman 6's labels:
// Podman 6 made `.Labels` in `podman ps --format` a list, and older kind's
// `kind get clusters` then fails or lists nothing
// (https://github.com/kubernetes-sigs/kind/issues/4201).
var kindForPodman6 = [3]int{0, 33, 0}

// checkKindForPodman compares the Podman client, which runs kind's `podman ps`
// templates, with the kind on PATH. Nothing to say for Docker, or when either
// version cannot be read.
func checkKindForPodman(engine cluster.ContainerEngine) []Result {
	if engine.Name != "podman" {
		return nil
	}
	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	podmanOut, err := exec.CommandContext(ctx, engine.Path, "--version").Output()
	if err != nil {
		return nil
	}
	kindOut, err := exec.CommandContext(ctx, kindPath, "version").Output()
	if err != nil {
		return nil
	}
	return evaluateKindForPodman(string(podmanOut), string(kindOut))
}

var versionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// parseVersion finds the first x.y.z in out ("podman version 6.2.0",
// "kind v0.32.0 go1.26.5 linux/amd64").
func parseVersion(out string) ([3]int, bool) {
	m := versionPattern.FindStringSubmatch(out)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

func evaluateKindForPodman(podmanOut, kindOut string) []Result {
	podman, okPodman := parseVersion(podmanOut)
	kind, okKind := parseVersion(kindOut)
	if !okPodman || !okKind || podman[0] < 6 {
		return nil
	}
	r := Result{Name: "kind for Podman 6", Detail: fmt.Sprintf("kind v%d.%d.%d, Podman %d.%d.%d", kind[0], kind[1], kind[2], podman[0], podman[1], podman[2])}
	if versionLess(kind, kindForPodman6) {
		r.Status = Warn
		r.Hint = fmt.Sprintf("kind before v%d.%d.%d cannot find its clusters with Podman 6 — upgrade kind: https://kind.sigs.k8s.io/docs/user/quick-start/#installation", kindForPodman6[0], kindForPodman6[1], kindForPodman6[2])
	}
	return []Result{r}
}

// Read through an existing node so macOS checks the engine's Linux VM,
// not unrelated host sysctls. No helper container or privileged exec needed.
func checkKindKeyringQuota(engine cluster.ContainerEngine) []Result {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, engine.Path, "ps", "--filter", "label=io.x-k8s.kind.role=control-plane", "--format", "{{.Names}}").Output()
	if err != nil || len(strings.Fields(string(out))) == 0 {
		return nil
	}
	node := strings.Fields(string(out))[0]
	out, err = exec.CommandContext(ctx, engine.Path, "exec", "--", node, "cat", "/proc/key-users").Output()
	if err != nil {
		return []Result{{Status: Warn, Name: "Kind keyring quota", Detail: "could not read kernel key quota from " + node, Hint: "if pod startup reports 'session key: disk quota exceeded', inspect kernel.keys limits in the engine's Linux VM/host"}}
	}
	return evaluateKeyringQuota(string(out))
}

func evaluateKeyringQuota(out string) []Result {
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
		r := Result{Name: "Kind keyring quota", Detail: used + "/" + keyMax + " keys, " + bytesUsed + "/" + bytesMax + " bytes (shared across clusters)"}
		if limit < 20000 || byteLimit < 500000 || float64(usage)/float64(limit) >= 0.8 || float64(byteUsage)/float64(byteLimit) >= 0.8 {
			r.Status = Warn
			r.Hint = "multiple Kind clusters can exhaust this quota and prevent CoreDNS pods from starting; inspect kernel.keys.maxkeys/maxbytes inside the engine's Linux VM/host — https://github.com/kubernetes-sigs/kind/issues/3806"
		}
		return []Result{r}
	}
	return nil
}

// Minimums kind's known-issues page recommends for multi-node clusters;
// below them kubelet/kube-proxy fail with "too many open files".
const (
	minInotifyWatches   = 524288
	minInotifyInstances = 512
)

// CheckInotify reads the Linux inotify limits under procRoot (normally
// /proc). Only meaningful on a Linux host — on macOS/Windows the nodes run
// in Docker Desktop's/podman machine's own VM.
func CheckInotify(procRoot string) []Result {
	limits := []struct {
		file string
		min  int
	}{
		{"sys/fs/inotify/max_user_watches", minInotifyWatches},
		{"sys/fs/inotify/max_user_instances", minInotifyInstances},
	}
	var res []Result
	for _, l := range limits {
		name := "fs.inotify." + filepath.Base(l.file)
		data, err := os.ReadFile(filepath.Join(procRoot, l.file))
		if err != nil {
			res = append(res, Result{Status: Warn, Name: name, Detail: "could not read: " + err.Error()})
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			res = append(res, Result{Status: Warn, Name: name, Detail: "unparseable value " + strings.TrimSpace(string(data))})
			continue
		}
		r := Result{Name: name, Detail: strconv.Itoa(v)}
		if v < l.min {
			r.Status = Warn
			r.Hint = fmt.Sprintf("multi-node kind clusters fail with 'too many open files' below %d — sudo sysctl %s=%d (persist in /etc/sysctl.d/)", l.min, name, l.min)
		}
		res = append(res, r)
	}
	return res
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
