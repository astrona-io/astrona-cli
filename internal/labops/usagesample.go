package labops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/labstate"
)

// SampleLabUsage is the UsageSampler of `astrona usage-reporter`: it
// measures a lab's kind containers (with its linked clusters) or QEMU VMs
// once and records the sample locally (at most one per five seconds). ok is
// false when the lab could not be measured as a whole — partial totals are
// never reported as the whole lab. Polling only reads: `ps` and the
// container engine's `stats`, with deadlines and bounded output.
func SampleLabUsage(ctx context.Context, clusterName string, st *labstate.State) (labstate.ResourceUsage, bool) {
	groups, _ := readQEMUVMs()
	if vms := groups[clusterName]; len(vms) > 0 {
		u := qemuRunUsage(clusterName, st.StartedAt, vms, readQEMUUsage(ctx, groups))
		return u, u.Samples > 0
	}
	containers, provider, err := readKindEngine(ctx)
	if err != nil {
		return labstate.ResourceUsage{}, false
	}
	u := kindRunUsage(clusterName, st.StartedAt, containers, readContainerUsage(ctx, provider, containers))
	return u, u.Samples > 0
}

type usageSample struct{ cpu, memory float64 }

// boundedOutput keeps a broken engine or cluster from filling memory
// through polling: writes past 1 MiB fail.
type boundedOutput struct{ buffer bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 1024*1024 {
		return 0, io.ErrShortBuffer
	}
	return b.buffer.Write(p)
}

// readKindEngine lists every kind container of the first container engine
// that answers, grouped by cluster, and returns that engine's path.
func readKindEngine(ctx context.Context) (map[string][]KindContainer, string, error) {
	providers := []string{"docker", "podman"}
	if provider := os.Getenv("KIND_EXPERIMENTAL_PROVIDER"); provider != "" {
		if provider != "docker" && provider != "podman" {
			return nil, "", fmt.Errorf("invalid KIND_EXPERIMENTAL_PROVIDER: %q", provider)
		}
		providers = []string{provider}
	}
	var failures []string
	for _, provider := range providers {
		path, err := exec.LookPath(provider)
		if err != nil {
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		cmd := exec.CommandContext(readCtx, path, "ps", "-a", "--filter", "label=io.x-k8s.kind.cluster", "--format", `{{.Names}}|{{.State}}|{{.Label "io.x-k8s.kind.cluster"}}|{{.Label "io.x-k8s.kind.role"}}`)
		cmd.WaitDelay = time.Second
		var out boundedOutput
		cmd.Stdout = &out
		err = cmd.Run()
		cancel()
		if err == nil {
			return GroupKindContainers(out.buffer.String()), path, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", provider, err))
	}
	return nil, "", fmt.Errorf("container engine unavailable; use astrona setup / doctor (%s)", strings.Join(failures, "; "))
}

// readQEMUHandle reads a VM's saved handle without creating anything.
func readQEMUHandle(name string) (*config.QEMUHandle, error) {
	if err := config.ValidateName(name); err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filepath.Join(home, apphome.Name(), "qemu", name, "handle.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var h config.QEMUHandle
	if err := json.NewDecoder(io.LimitReader(f, 1024*1024)).Decode(&h); err != nil {
		return nil, fmt.Errorf("read VM %s: %w", name, err)
	}
	if h.ClusterName != name {
		return nil, fmt.Errorf("VM state does not match %s", name)
	}
	if h.LabName == "" {
		h.LabName = name
	}
	if err := config.ValidateName(h.LabName); err != nil || (h.LabName != name && !strings.HasPrefix(name, h.LabName+"-")) {
		return nil, fmt.Errorf("VM %s has an invalid lab owner", name)
	}
	return &h, nil
}

// readQEMUVMs groups the saved VM handles by the lab they belong to.
func readQEMUVMs() (map[string][]*config.QEMUHandle, error) {
	groups := map[string][]*config.QEMUHandle{}
	home, err := os.UserHomeDir()
	if err != nil {
		return groups, err
	}
	entries, err := os.ReadDir(filepath.Join(home, apphome.Name(), "qemu"))
	if os.IsNotExist(err) {
		return groups, nil
	}
	if err != nil {
		return groups, err
	}
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), apphome.ClusterPrefix()) || strings.HasPrefix(entry.Name(), apphome.TestClusterPrefix()) {
			continue
		}
		h, err := readQEMUHandle(entry.Name())
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		groups[h.LabName] = append(groups[h.LabName], h)
	}
	return groups, errors.Join(failures...)
}

func readQEMUUsage(ctx context.Context, groups map[string][]*config.QEMUHandle) map[int]usageSample {
	expected := map[int]string{}
	var pids []string
	for _, vms := range groups {
		for _, vm := range vms {
			if vm.PID > 0 && expected[vm.PID] == "" {
				expected[vm.PID] = vm.ClusterName
				pids = append(pids, strconv.Itoa(vm.PID))
			}
		}
	}
	if len(pids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ps", "-ww", "-p", strings.Join(pids, ","), "-o", "pid=", "-o", "%cpu=", "-o", "rss=", "-o", "command=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.WaitDelay = time.Second
	var out boundedOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	return parseQEMUUsage(out.buffer.String(), expected)
}

func parseQEMUUsage(output string, expected map[int]string) map[int]usageSample {
	samples := map[int]usageSample{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || !strings.Contains(fields[3], "qemu-system") {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || expected[pid] == "" {
			continue
		}
		matches := false
		for i := 4; i+1 < len(fields); i++ {
			if fields[i] == "-name" && fields[i+1] == "astrona-"+expected[pid] {
				matches = true
			}
		}
		cpu, cpuErr := strconv.ParseFloat(fields[1], 64)
		rss, rssErr := strconv.ParseFloat(fields[2], 64)
		if !matches || cpuErr != nil || rssErr != nil || cpu < 0 || rss < 0 || math.IsNaN(cpu) || math.IsNaN(rss) || math.IsInf(cpu, 0) || math.IsInf(rss*1024, 0) {
			continue
		}
		samples[pid] = usageSample{cpu, rss * 1024}
	}
	return samples
}

func qemuRunUsage(name, started string, vms []*config.QEMUHandle, samples map[int]usageSample) labstate.ResourceUsage {
	if started == "" || len(vms) == 0 {
		return labstate.ResourceUsage{}
	}
	var cpu, memory float64
	seen := map[int]bool{}
	for _, vm := range vms {
		sample, ok := samples[vm.PID]
		if !ok || seen[vm.PID] {
			return labstate.ResourceUsage{}
		}
		seen[vm.PID] = true
		cpu += sample.cpu
		memory += sample.memory
	}
	usage, err := labstate.RecordUsage(name, started, cpu, memory, time.Now())
	if err != nil {
		return labstate.ResourceUsage{}
	}
	return usage
}

func readContainerUsage(ctx context.Context, provider string, containers map[string][]KindContainer) map[string]usageSample {
	names := []string{}
	for _, nodes := range containers {
		for _, node := range nodes {
			if node.State == "running" {
				names = append(names, node.Name)
			}
		}
	}
	if provider == "" || len(names) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	args := append([]string{"stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}", "--"}, names...)
	cmd := exec.CommandContext(ctx, provider, args...)
	cmd.WaitDelay = time.Second
	var out boundedOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	return parseContainerUsage(out.buffer.String())
}

func parseContainerUsage(output string) map[string]usageSample {
	result := map[string]usageSample{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Split(line, "|")
		if len(parts) != 3 {
			continue
		}
		cpu, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(parts[1]), "%"), 64)
		memory, ok := memoryBytes(strings.SplitN(parts[2], "/", 2)[0])
		if err != nil || !ok || cpu < 0 || math.IsNaN(cpu) || math.IsInf(cpu, 0) {
			continue
		}
		result[strings.TrimSpace(parts[0])] = usageSample{cpu, memory}
	}
	return result
}

func memoryBytes(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	split := strings.IndexFunc(value, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if split <= 0 {
		return 0, false
	}
	amount, err := strconv.ParseFloat(value[:split], 64)
	scale, ok := map[string]float64{"B": 1, "kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}[strings.TrimSpace(value[split:])]
	return amount * scale, ok && err == nil && amount >= 0 && !math.IsInf(amount, 0) && !math.IsNaN(amount)
}

func kindRunUsage(name, started string, containers map[string][]KindContainer, samples map[string]usageSample) labstate.ResourceUsage {
	if started == "" {
		return labstate.ResourceUsage{}
	}
	names := []string{name}
	links, err := cluster.ReadLinks(name)
	if err != nil {
		return labstate.ResourceUsage{}
	}
	for _, link := range links {
		names = append(names, link.Cluster)
	}
	var cpu, memory float64
	seen := map[string]bool{}
	for _, name := range names {
		nodes := containers[name]
		if len(nodes) == 0 {
			return labstate.ResourceUsage{}
		}
		for _, node := range nodes {
			if seen[node.Name] {
				continue
			}
			seen[node.Name] = true
			sample, ok := samples[node.Name]
			if !ok {
				return labstate.ResourceUsage{} // never report partial totals as the whole lab
			}
			cpu += sample.cpu
			memory += sample.memory
		}
	}
	usage, err := labstate.RecordUsage(name, started, cpu, memory, time.Now())
	if err != nil {
		return labstate.ResourceUsage{}
	}
	return usage
}
