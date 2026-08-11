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
// about (from ~/.astrona/qemu) plus any kind clusters on the host, so a
// developer can see what's actually running before deciding what to
// `astrona destroy`. The qemu runtime especially needs this — it's a raw
// process astrona itself manages, not something already listable via
// `docker ps`/`kind get clusters`, so a forgotten VM is easy to leave
// running ("ghost") with nothing else on the machine surfacing it.
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

// listKindClusters best-effort lists kind clusters via `kind get clusters`.
// Silently skipped if kind isn't installed or the command fails — this is a
// convenience addition to `list`, not something list itself owns the way it
// owns qemu state.
func listKindClusters() {
	kindPath, err := exec.LookPath("kind")
	if err != nil {
		return
	}

	out, err := exec.Command(kindPath, "get", "clusters").Output()
	if err != nil {
		return
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || strings.HasPrefix(trimmed, "No kind clusters") {
		return
	}

	fmt.Println("\nkind clusters:")
	for _, name := range strings.Fields(trimmed) {
		fmt.Printf("  %s  %s\n", colorize(ansiGreen, "●"), name)
	}
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
