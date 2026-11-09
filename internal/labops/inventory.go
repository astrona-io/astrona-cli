package labops

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/hypervisor"
	"astrona/internal/labstate"
	"astrona/internal/portforward"
)

// LabRow is one lab astrona knows on this machine — one qemu VM or one
// kind cluster, shaped the same way regardless of runtime so `astrona
// list` prints both through the same table.
type LabRow struct {
	Name    string
	Runtime string
	Status  string
	Uptime  string
	NICs    string
	Details string
	Lab     string // qemu: the lab the VM belongs to ("" if its handle predates recording it)

	// Filled for kind by EnrichKindHealth (`astrona list` only — other
	// callers just need names); "-" when unknown/not applicable.
	Version  string // Kubernetes version (kubelet) of the nodes
	Forwards string // e.g. "2/2 Ready"
	Current  bool   // the lab picked with `astrona use`
}

// CollectQEMURows gathers a LabRow for every live qemu VM found under
// ~/.astrona/qemu, and separately counts (without deleting) stale state dirs
// left behind by a VM that's no longer running — the state dir alone doesn't
// mean the VM is still up, LoadQEMUHandle's processAlive check is the only
// source of truth.
func CollectQEMURows() ([]LabRow, int, error) {
	base, err := hypervisor.QEMUBaseDir()
	if err != nil {
		return nil, 0, err
	}

	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to read qemu state dir '%s': %w", base, err)
	}

	var rows []LabRow
	stale := 0

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		if !strings.HasPrefix(e.Name(), apphome.ClusterPrefix()) {
			continue
		}

		data, err := os.ReadFile(filepath.Join(base, e.Name(), "handle.json"))
		if err != nil {
			continue
		}

		var h config.QEMUHandle
		if err := json.Unmarshal(data, &h); err != nil {
			continue
		}

		if !hypervisor.ProcessAlive(h.PID) {
			stale++
			continue
		}

		uptime := "unknown"
		if !h.StartedAt.IsZero() {
			uptime = FormatUptime(time.Since(h.StartedAt))
		}

		rows = append(rows, LabRow{
			Name:    h.ClusterName,
			Runtime: "qemu",
			Status:  "Running",
			Uptime:  uptime,
			NICs:    formatNICs(h),
			Details: fmt.Sprintf("ssh %s@%s -p %d", h.SSHUser, h.SSHHost, h.SSHPort),
			Lab:     h.LabName,
		})
	}

	return rows, stale, nil
}

// CollectKindRows gathers a LabRow for every astrona-generated kind cluster
// by querying the container engine directly for containers carrying kind's
// own "io.x-k8s.kind.cluster" label, rather than shelling out to `kind get
// clusters`. Deliberate: `kind get clusters` has a known bug against podman
// where its output template fails on podman's `ps` JSON shape (confirmed on
// this repo's own dev machine — "error calling index: cannot index
// slice/array with type string"), which would make this permanently broken
// on any podman-only host. kind always names a cluster's control-plane
// container "<cluster>-control-plane" regardless of provider, so filtering
// on the label and stripping that suffix gets the same answer without going
// through kind's broken path — and conveniently, `docker`/`podman inspect`
// on that same container name is exactly how uptime is derived too.
//
// Filtered to show only names starting with the "astro-" prefix, so only
// astrona-generated kind clusters are listed.
//
// An engine that can't be queried counts as no kind labs here; callers that
// must tell that apart from "none" use LookupKindRows.
func CollectKindRows() []LabRow {
	rows, _ := LookupKindRows()
	return rows
}

// LookupKindRows is CollectKindRows, but an installed container engine that
// doesn't answer (Docker or Podman stopped) is an error rather than an
// empty list. No engine installed at all is no kind labs: nil, nil.
func LookupKindRows() ([]LabRow, error) {
	engine, err := cluster.DetectContainerEngine()
	if errors.Is(err, cluster.ErrEngineNotInstalled) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// {{.Label "k"}} works on both docker and podman; podman's
	// {{index .Labels "k"}} doesn't (same template quirk as above).
	out, err := exec.Command(engine.Path, "ps", "-a",
		"--filter", "label=io.x-k8s.kind.cluster",
		"--format", `{{.Names}}|{{.State}}|{{.Label "io.x-k8s.kind.cluster"}}|{{.Label "io.x-k8s.kind.role"}}`).Output()
	if err != nil {
		return nil, fmt.Errorf("%s ps failed: %w", engine.Name, err)
	}

	clusters := GroupKindContainers(string(out))
	names := make([]string, 0, len(clusters))
	for name := range clusters {
		names = append(names, name)
	}
	sort.Strings(names)

	rows := make([]LabRow, 0, len(names))
	for _, name := range names {
		status := ContainerStatus(clusters[name])
		uptime := "unknown"
		if status == "Stopped" {
			uptime = "-"
		} else if started, err := containerStartedAt(engine, name+"-control-plane"); err == nil {
			uptime = FormatUptime(time.Since(started))
		}

		details := "kubectl --context kind-" + name
		if n := portforward.Count(name); n > 0 {
			details += fmt.Sprintf(" (+%d port-forward(s): astrona pf list %s)", n, name)
		}

		rows = append(rows, LabRow{
			Name:    name,
			Runtime: "kind",
			Status:  status,
			Uptime:  uptime,
			NICs:    "-", // kind's networking is the container engine's pod/service network, not a per-lab NIC concept astrona owns
			Details: details,
		})
	}

	return rows, nil
}

// engineUnreachableStatus is the STATUS of a remembered lab `astrona list`
// can't check because the container engine doesn't answer.
const engineUnreachableStatus = "EngineUnreachable"

// UnreachableKindRows are the remembered labs (~/.astrona/labs) that have
// no qemu VM, shown when the container engine can't be asked about kind
// clusters — such a lab may well still exist, so it's listed, never
// forgotten. qemu holds the qemu rows already found.
func UnreachableKindRows(qemu []LabRow) []LabRow {
	names, _ := labstate.Names()
	seen := map[string]bool{}
	for _, r := range qemu {
		seen[r.Name] = true
		if r.Lab != "" {
			seen[r.Lab] = true
		}
	}
	var rows []LabRow
	for _, n := range names {
		if seen[n] || hypervisor.StateExists(n) || len(hypervisor.MultiVMs(n)) > 0 {
			continue
		}
		rows = append(rows, LabRow{
			Name:    n,
			Runtime: "kind",
			Status:  engineUnreachableStatus,
			Uptime:  "-",
			NICs:    "-",
			Details: "start Docker or Podman to check this lab",
		})
	}
	return rows
}

// KindContainer is one node (or load balancer) container of a kind
// cluster, as `docker/podman ps` reports it.
type KindContainer struct {
	Name, State, Role string
}

// GroupKindContainers parses LookupKindRows' ps output into astrona
// clusters (the "astro-" prefix) → their containers.
func GroupKindContainers(out string) map[string][]KindContainer {
	clusters := map[string][]KindContainer{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 4 || !strings.HasPrefix(parts[2], apphome.ClusterPrefix()) {
			continue
		}
		clusters[parts[2]] = append(clusters[parts[2]], KindContainer{Name: parts[0], State: strings.ToLower(parts[1]), Role: parts[3]})
	}
	return clusters
}

// ContainerStatus is the status a cluster gets from its containers alone:
// "Running" (all up — EnrichKindHealth refines it), "Stopped" (none up),
// or "Degraded (r/n containers running)".
func ContainerStatus(cs []KindContainer) string {
	running := 0
	for _, c := range cs {
		if c.State == "running" {
			running++
		}
	}
	switch running {
	case len(cs):
		return "Running"
	case 0:
		return "Stopped"
	default:
		return fmt.Sprintf("Degraded (%d/%d containers running)", running, len(cs))
	}
}

// kindHealthTimeout bounds each cluster's API query, so one wedged
// cluster can't stall `astrona list`.
const kindHealthTimeout = "3s"

// EnrichKindHealth replaces each running kind cluster's "Running" with
// live API health and fills version/forwards — queried in parallel, one
// kubectl call per cluster.
func EnrichKindHealth(rows []LabRow) []LabRow {
	var wg sync.WaitGroup
	for i := range rows {
		if rows[i].Runtime != "kind" {
			continue
		}
		rows[i].Forwards = forwardSummary(rows[i].Name)
		if rows[i].Status != "Running" {
			continue
		}
		wg.Add(1)
		go func(r *LabRow) {
			defer wg.Done()
			r.Status, r.Version = KindAPIHealth(r.Name)
		}(&rows[i])
	}
	wg.Wait()
	return rows
}

// KindAPIHealth asks the cluster's API for its nodes: NodeHealth's status
// and version, or "Unreachable" when kubectl gets no answer.
func KindAPIHealth(name string) (status, version string) {
	cmd := exec.Command("kubectl", "--context", "kind-"+name, "get", "nodes", "-o", "json", "--request-timeout="+kindHealthTimeout)
	cmd.Env = executor.KubeconfigEnv(cluster.ExistingKubeconfig(name))
	out, err := cmd.Output()
	if err != nil {
		return "Unreachable", ""
	}
	return NodeHealth(out)
}

type nodeListJSON struct {
	Items []struct {
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
			NodeInfo struct {
				KubeletVersion string `json:"kubeletVersion"`
			} `json:"nodeInfo"`
		} `json:"status"`
	} `json:"items"`
}

// NodeHealth summarizes `kubectl get nodes -o json`: "Ready (n/n)" or
// "NotReady (r/n)", plus the (first node's) Kubernetes version.
func NodeHealth(data []byte) (status, version string) {
	var nodes nodeListJSON
	if err := json.Unmarshal(data, &nodes); err != nil || len(nodes.Items) == 0 {
		return "Unreachable", ""
	}
	ready := 0
	for _, n := range nodes.Items {
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				ready++
			}
		}
	}
	version = nodes.Items[0].Status.NodeInfo.KubeletVersion
	if ready == len(nodes.Items) {
		return fmt.Sprintf("Ready (%d/%d)", ready, len(nodes.Items)), version
	}
	return fmt.Sprintf("NotReady (%d/%d)", ready, len(nodes.Items)), version
}

// forwardSummary is "<ready>/<total> Ready" for name's port forwards, ""
// when it has none.
func forwardSummary(name string) string {
	fs, err := portforward.List(name)
	if err != nil || len(fs) == 0 {
		return ""
	}
	ready := 0
	for _, f := range fs {
		if f.Effective() == portforward.StateReady {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d Ready", ready, len(fs))
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
func containerStartedAt(engine cluster.ContainerEngine, containerName string) (time.Time, error) {
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

// formatNICs summarizes a qemu VM's NICs: the implicit mgmt NIC (net0,
// host-only, always present — it's astrona's only control channel for
// bootstrap/validate/ssh/submit/destroy, not part of a lab's own topology)
// spelled out by name rather than folded into a bare count, plus every
// extra NIC's segment name and static IP.
func formatNICs(h config.QEMUHandle) string {
	parts := make([]string, 0, 1+len(h.Networks))
	parts = append(parts, "mgmt")
	for _, n := range h.Networks {
		parts = append(parts, fmt.Sprintf("%s=%s", n.Name, n.IP))
	}
	return fmt.Sprintf("%d (%s)", len(parts), strings.Join(parts, ", "))
}

// FormatUptime renders d as the coarsest human unit that fits (Xh Ym, Xm Ys,
// or Xs) — a VM's uptime doesn't need sub-second precision.
func FormatUptime(d time.Duration) string {
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

// LinkedClusterOwners maps every running linked cluster to the lab it
// belongs to — so discovery and lab pickers treat it as part of that lab,
// not as a lab of its own.
func LinkedClusterOwners() map[string]string {
	owners := map[string]string{}
	for _, r := range CollectKindRows() {
		links, _ := cluster.ReadLinks(r.Name)
		for _, l := range links {
			owners[l.Cluster] = r.Name
		}
	}
	return owners
}

// MarkLinkedClusters notes which rows are a lab's linked cluster
// (runtime.kind.clusters) — managed with that lab, not on their own.
func MarkLinkedClusters(rows []LabRow, owners map[string]string) {
	for i := range rows {
		if owner, ok := owners[rows[i].Name]; ok {
			rows[i].Details = "linked cluster of " + owner + " · " + rows[i].Details
		}
	}
}

// MarkCurrentLab marks the lab picked with `astrona use`; current is its
// name as remembered (metadata.name), "" when none is picked.
func MarkCurrentLab(rows []LabRow, current string) {
	if current == "" {
		return
	}
	name := config.NormalizeClusterName(current)
	for i := range rows {
		if rows[i].Name == name {
			rows[i].Current = true
			rows[i].Details += " · current lab (astrona use)"
		}
	}
}
