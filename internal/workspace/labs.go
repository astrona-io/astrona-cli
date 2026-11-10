package workspace

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/labops"
	"astrona/internal/labstate"
)

// GET /api/v1/labs lists every astrona lab on this machine, not only the
// ones opened in the browser: `astrona run` returns while its lab keeps
// running in the background, so the page's "Labs on this computer" shows
// what still uses the machine's CPU and memory, and how to end it.

// machineLab is one lab in GET /labs.
type machineLab struct {
	// Lab is the full name (cluster name for Kind, lab name for QEMU).
	Lab string `json:"lab"`
	// Name is the short name for commands (astrona destroy <name>).
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Runtime string `json:"runtime"`
	// State is the environment as the container engine or hypervisor sees
	// it: running, stopped, degraded or unknown (the engine does not answer).
	State  string `json:"state"`
	Uptime string `json:"uptime,omitempty"`
	VMs    int    `json:"vms,omitempty"`
	// Mode, Playground and DeadlineAt come from the lab's session.
	Mode       string `json:"mode,omitempty"`
	Playground bool   `json:"playground"`
	DeadlineAt string `json:"deadlineAt,omitempty"`
	// Workspace is true when the browser workspace has this run: the page
	// can open it, and end or remove it, directly. RunID goes with it.
	Workspace bool   `json:"workspace"`
	RunID     string `json:"runId,omitempty"`
	// RunStatus is the workspace status (ready, running, expired, …).
	RunStatus string `json:"runStatus,omitempty"`
}

func kindState(status string) string {
	switch {
	case status == "Stopped":
		return "stopped"
	case strings.HasPrefix(status, "Degraded"):
		return "degraded"
	case status == "EngineUnreachable":
		return "unknown"
	}
	return "running"
}

func (s *Server) handleLabs(w http.ResponseWriter, _ *http.Request) {
	qemuRows, _, _ := labops.CollectQEMURows()
	kindRows, engineErr := labops.LookupKindRows()
	if engineErr != nil {
		kindRows = labops.UnreachableKindRows(qemuRows)
	}
	owners := labops.LinkedClusterOwners()
	known, _ := labstate.Names()
	test := apphome.TestClusterPrefix()

	byLab := map[string]*machineLab{}
	var order []string
	add := func(lab string) *machineLab {
		if l, ok := byLab[lab]; ok {
			return l
		}
		l := &machineLab{Lab: lab, Name: strings.TrimPrefix(lab, apphome.ClusterPrefix())}
		byLab[lab] = l
		order = append(order, lab)
		return l
	}
	for _, r := range kindRows {
		if _, linked := owners[r.Name]; linked || strings.HasPrefix(r.Name, test) {
			continue
		}
		l := add(r.Name)
		l.Runtime, l.State = "kind", kindState(r.Status)
		if r.Uptime != "" && r.Uptime != "-" && r.Uptime != "unknown" {
			l.Uptime = r.Uptime
		}
	}
	for _, r := range qemuRows {
		if strings.HasPrefix(r.Name, test) {
			continue
		}
		l := add(labops.LabOfVM(r.Name, known))
		l.Runtime, l.State = "qemu", "running"
		l.VMs++
		if l.Uptime == "" && r.Uptime != "-" {
			l.Uptime = r.Uptime
		}
	}

	now := time.Now()
	labs := make([]machineLab, 0, len(order))
	for _, lab := range order {
		l := byLab[lab]
		if st, err := labstate.Load(lab); err == nil && st != nil && st.Session != nil {
			l.Playground = st.Session.IsPlayground()
			l.DeadlineAt = st.Session.DeadlineAt
		}
		if rec, err := ReadRun(lab); err == nil {
			if v, err := buildView(rec, now); err == nil && v.Status != StatusStopped {
				l.Title, l.Mode, l.Playground = rec.Title, v.Mode, rec.Playground
				l.Workspace, l.RunID, l.RunStatus = true, rec.RunID, v.Status
				if v.DeadlineAt != nil {
					l.DeadlineAt = v.DeadlineAt.UTC().Format(time.RFC3339)
				}
			}
		}
		if l.Playground && l.Mode == "" {
			l.Mode = "playground"
		}
		labs = append(labs, *l)
	}
	sort.SliceStable(labs, func(i, j int) bool { return labs[i].Lab < labs[j].Lab })
	resp := map[string]any{"labs": labs}
	if engineErr != nil {
		resp["engine"] = "The container engine (Docker or Podman) does not answer, so Kind labs cannot be checked. Start it to see their state."
	}
	writeJSON(w, http.StatusOK, resp)
}
