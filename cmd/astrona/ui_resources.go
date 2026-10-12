package main

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/labstate"
)

type uiContainerUsage struct{ cpu, memory float64 }

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
