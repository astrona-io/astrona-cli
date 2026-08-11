package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newListCmd builds `astrona list`: enumerates every qemu lab astrona knows
// about (from ~/.astrona/qemu, with PID/SSH/uptime) plus every kind cluster
// on the host (queried straight from the container engine, with uptime from
// its control-plane container — see listKindClusters), so a developer can
// see what's actually running before
// deciding what to `astrona destroy`. The qemu runtime especially needs
// this — it's a raw process astrona itself manages, not something already
// listable via `docker ps`/`kind get clusters`, so a forgotten VM is easy to
// leave running ("ghost") with nothing else on the machine surfacing it.
// kind clusters get listed too for the same "what's actually running"
// visibility, though — unlike qemu — kind keeps no marker of which clusters
// astrona itself created vs. one made by hand, so every kind cluster on the
// host shows up here (see listKindClusters).
func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List astrona labs (qemu VMs and kind clusters) known on this machine",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := listQEMULabs(); err != nil {
				return err
			}
			listKindClusters()
			return nil
		},
	}
}

// listQEMULabs prints every live qemu VM found under ~/.astrona/qemu, and
// separately counts (without deleting) stale state dirs left behind by a VM
// that's no longer running — the state dir alone doesn't mean the VM is
// still up, LoadQEMUHandle's processAlive check is the only source of truth.
func listQEMULabs() error {
	base, err := qemuBaseDir()
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		return fmt.Errorf("failed to read qemu state dir '%s': %w", base, err)
	}

	fmt.Println("qemu VMs:")
	running := 0
	stale := 0

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		data, err := os.ReadFile(filepath.Join(base, e.Name(), "handle.json"))
		if err != nil {
			continue
		}

		var h QEMUHandle
		if err := json.Unmarshal(data, &h); err != nil {
			continue
		}

		if !processAlive(h.PID) {
			stale++
			continue
		}

		running++
		uptime := "unknown"
		if !h.StartedAt.IsZero() {
			uptime = formatUptime(time.Since(h.StartedAt))
		}
		fmt.Printf("  %s  %-24s pid=%-8d ssh=%s@%s:%-6d uptime=%s\n",
			colorize(ansiGreen, "●"), h.ClusterName, h.PID, h.SSHUser, h.SSHHost, h.SSHPort, uptime)
	}

	if running == 0 {
		fmt.Println("  (none running)")
	}
	if stale > 0 {
		fmt.Printf("  (%d stale state dir(s) left behind by a VM that's no longer running — `astrona destroy -c <lab>` to clean up)\n", stale)
	}

	return nil
}

// listKindClusters lists kind clusters by querying the container engine
// directly for containers carrying kind's own "io.x-k8s.kind.cluster"
// label, rather than shelling out to `kind get clusters`. Deliberate: `kind
// get clusters` has a known bug against podman where its output template
// fails on podman's `ps` JSON shape (confirmed on this repo's own dev
// machine — "error calling index: cannot index slice/array with type
// string"), which would make this section of `list` permanently broken on
// any podman-only host. kind always names a cluster's control-plane
// container "<cluster>-control-plane" regardless of provider, so filtering
// on the label and stripping that suffix gets the same answer without going
// through kind's broken path — and conveniently, `docker`/`podman inspect`
// on that same container name is exactly how uptime is derived too.
//
// Important caveat printed alongside the results: nothing here distinguishes
// a cluster astrona created from one made by hand (`kind create cluster`)
// — every kind cluster on the host shows up, astrona-owned or not. Unlike
// qemu (whose process -name is prefixed "astrona-<name>"), nothing marks a
// kind cluster as astrona's.
func listKindClusters() {
	fmt.Println("\nkind clusters:")

	engine, err := DetectContainerEngine()
	if err != nil {
		fmt.Println("  (no container engine found, skipping)")
		return
	}

	out, err := exec.Command(engine.Path, "ps", "-a",
		"--filter", "label=io.x-k8s.kind.cluster",
		"--format", "{{.Names}}").Output()
	if err != nil {
		fmt.Printf("  (failed to query %s: %s)\n", engine.Name, err)
		return
	}

	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name, ok := strings.CutSuffix(strings.TrimSpace(line), "-control-plane"); ok && name != "" {
			names = append(names, name)
		}
	}

	if len(names) == 0 {
		fmt.Println("  (none running)")
		return
	}

	for _, name := range names {
		uptime := "unknown"
		if started, err := containerStartedAt(engine, name+"-control-plane"); err == nil {
			uptime = formatUptime(time.Since(started))
		}
		fmt.Printf("  %s  %-24s uptime=%s\n", colorize(ansiGreen, "●"), name, uptime)
	}

	fmt.Println("  (kind tracks no astrona-ownership marker — every kind cluster on this host is listed, not just astrona's)")
}

// containerTimeLayouts covers both engines' `inspect -f '{{.State.StartedAt}}'`
// output: docker prints RFC3339Nano, podman prints Go's default
// time.Time.String() layout (e.g. "2026-08-11 21:14:17.140921448 +0200
// CEST") — different enough that one layout can't parse both.
var containerTimeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999 -0700 MST",
}

// containerStartedAt reads a container's start time via `docker`/`podman
// inspect` (same `-f`/Go-template syntax for both), used to derive a kind
// cluster's uptime from its control-plane container.
func containerStartedAt(engine ContainerEngine, containerName string) (time.Time, error) {
	out, err := exec.Command(engine.Path, "inspect", "-f", "{{.State.StartedAt}}", containerName).Output()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to inspect container '%s': %w", containerName, err)
	}

	trimmed := strings.TrimSpace(string(out))

	var lastErr error
	for _, layout := range containerTimeLayouts {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return t, nil
		} else {
			lastErr = err
		}
	}

	return time.Time{}, fmt.Errorf("failed to parse container start time '%s': %w", trimmed, lastErr)
}

// formatUptime renders d as the coarsest human unit that fits (Xh Ym, Xm Ys,
// or Xs) — a VM's uptime doesn't need sub-second precision.
func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
