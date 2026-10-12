package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/labstate"
)

type uiContainerUsage struct{ cpu, memory float64 }

func readUIQEMUUsage(ctx context.Context, groups map[string][]*config.QEMUHandle) map[int]uiContainerUsage {
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
	var out workspaceOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	return parseUIQEMUUsage(string(out.Bytes()), expected)
}

func parseUIQEMUUsage(output string, expected map[int]string) map[int]uiContainerUsage {
	samples := map[int]uiContainerUsage{}
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
		samples[pid] = uiContainerUsage{cpu, rss * 1024}
	}
	return samples
}

func uiQEMURunUsage(name, started string, vms []*config.QEMUHandle, samples map[int]uiContainerUsage) labstate.ResourceUsage {
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

func readUIContainerUsage(ctx context.Context, provider string, containers map[string][]kindContainer) map[string]uiContainerUsage {
	names := []string{}
	for _, nodes := range containers {
		for _, node := range nodes {
			if node.state == "running" {
				names = append(names, node.name)
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
	var out workspaceOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil
	}
	return parseUIContainerUsage(string(out.Bytes()))
}

func parseUIContainerUsage(output string) map[string]uiContainerUsage {
	result := map[string]uiContainerUsage{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Split(line, "|")
		if len(parts) != 3 {
			continue
		}
		cpu, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(parts[1]), "%"), 64)
		memory, ok := uiMemoryBytes(strings.SplitN(parts[2], "/", 2)[0])
		if err != nil || !ok || cpu < 0 || math.IsNaN(cpu) || math.IsInf(cpu, 0) {
			continue
		}
		result[strings.TrimSpace(parts[0])] = uiContainerUsage{cpu, memory}
	}
	return result
}

func uiMemoryBytes(value string) (float64, bool) {
	value = strings.TrimSpace(value)
	split := strings.IndexFunc(value, func(r rune) bool { return (r < '0' || r > '9') && r != '.' })
	if split <= 0 {
		return 0, false
	}
	amount, err := strconv.ParseFloat(value[:split], 64)
	scale, ok := map[string]float64{"B": 1, "kB": 1e3, "KB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40}[strings.TrimSpace(value[split:])]
	return amount * scale, ok && err == nil && amount >= 0 && !math.IsInf(amount, 0) && !math.IsNaN(amount)
}

func uiRunUsage(name, started string, containers map[string][]kindContainer, samples map[string]uiContainerUsage) labstate.ResourceUsage {
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
			if seen[node.name] {
				continue
			}
			seen[node.name] = true
			sample, ok := samples[node.name]
			if !ok {
				return labstate.ResourceUsage{}
			} // Never report partial totals as the whole lab.
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

func uiUsageText(usage labstate.ResourceUsage) string {
	if usage.Samples == 0 {
		return "CPU / memory: unavailable"
	}
	return fmt.Sprintf("CPU %.1f%% (avg %.1f%%) · Memory %s (avg %s)", usage.CPU, usage.AverageCPU, uiMemoryText(usage.Memory), uiMemoryText(usage.AverageMemory))
}

func uiMemoryText(bytes float64) string {
	if bytes >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", bytes/(1<<30))
	}
	return fmt.Sprintf("%.0f MiB", bytes/(1<<20))
}
