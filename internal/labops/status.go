package labops

import (
	"fmt"
	"strings"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/lifecycle"
	"astrona/internal/portforward"
	"astrona/internal/proctor"
)

// LabStatus is everything `astrona status` shows about one lab, gathered
// from state astrona already keeps (no new state of its own).
type LabStatus struct {
	Row      LabRow
	Cfg      *config.LabConfig // nil when no lab config was found
	Forwards []portforward.Forward
	Links    []cluster.LinkState
	LinkRows map[string]LabRow // linked cluster → its row (status), when running
	Exam     *exam.State
	Attempts []proctor.Attempt
	Now      time.Time
}

// FindLabRow returns the `astrona list` row for name (with live kind
// health), or false if no such lab is running.
func FindLabRow(name string) (LabRow, bool) {
	qemu, _, _ := CollectQEMURows()
	for _, r := range qemu {
		if r.Name == name {
			return r, true
		}
	}
	for _, r := range CollectKindRows() {
		if r.Name == name {
			return EnrichKindHealth([]LabRow{r})[0], true
		}
	}
	return LabRow{}, false
}

// ResolveStatusLab picks the lab: an explicit name, else the lab config
// cmd loaded from -c/--file/--git (cfg, nil when none loaded; cleanup
// releases it), else the only astrona lab on the machine. The returned
// cleanup is always safe to call.
func ResolveStatusLab(labArg string, cfg *config.LabConfig, cleanup func()) (string, *config.LabConfig, func(), error) {
	noop := func() {}
	if cleanup == nil {
		cleanup = noop
	}

	switch {
	case labArg != "":
		name := config.NormalizeClusterName(labArg)
		if cfg != nil && config.NormalizeClusterName(cfg.Metadata.Name) != name {
			cleanup()
			cfg, cleanup = nil, noop // config is for another lab
		}
		return name, cfg, cleanup, nil
	case cfg != nil:
		return config.NormalizeClusterName(cfg.Metadata.Name), cfg, cleanup, nil
	}

	qemu, _, _ := CollectQEMURows()
	var names []string
	for _, r := range append(qemu, CollectKindRows()...) {
		if !strings.HasPrefix(r.Name, apphome.TestClusterPrefix()) {
			names = append(names, r.Name)
		}
	}
	switch len(names) {
	case 0:
		return "", nil, noop, fmt.Errorf("no astrona lab is running — start one with `astrona run`")
	case 1:
		return names[0], nil, noop, nil
	default:
		return "", nil, noop, fmt.Errorf("several labs are running — pick one: %s", strings.Join(names, ", "))
	}
}

// Status gathers the status of the running lab name (cfg is its lab
// config, or nil) as of now. A lab that isn't running is an error.
func Status(name string, cfg *config.LabConfig, now time.Time) (StatusResult, error) {
	row, ok := FindLabRow(name)
	if !ok {
		return StatusResult{}, fmt.Errorf("lab '%s' isn't running — `astrona run` starts it, `astrona list` shows what is running", name)
	}

	st := LabStatus{Row: row, Cfg: cfg, Now: now}
	if row.Runtime == "kind" {
		st.Forwards, _ = portforward.List(name)
	}
	st.Exam, _ = exam.Load(name)
	st.Attempts, _ = proctor.LoadAttempts(name)
	st.Links, _ = lifecycle.Links(name)
	st.LinkRows = map[string]LabRow{}
	for _, l := range st.Links {
		if r, ok := FindLabRow(l.Cluster); ok {
			st.LinkRows[l.Cluster] = r
		}
	}
	return st.Result(), nil
}

// StatusResult is `astrona status` as -o json prints it. Lab keeps the
// gathered facts the text view renders; it is not part of the JSON.
type StatusResult struct {
	Name           string              `json:"name"`
	Runtime        string              `json:"runtime"`
	Status         string              `json:"status"`
	Kubernetes     string              `json:"kubernetes,omitempty"`
	Uptime         string              `json:"uptime,omitempty"`
	Context        string              `json:"context,omitempty"`
	PortForwards   []ForwardJSON       `json:"portForwards"`
	LinkedClusters []LinkedClusterJSON `json:"linkedClusters"`
	Exam           *ExamJSON           `json:"exam,omitempty"`
	Attempts       int                 `json:"attempts"`
	LastAttempt    *proctor.Attempt    `json:"lastAttempt,omitempty"`
	Next           string              `json:"next"`

	Lab LabStatus `json:"-"`
}

// LinkedClusterJSON is a linked cluster in `astrona status -o json`.
type LinkedClusterJSON struct {
	Name     string `json:"name"`
	Cluster  string `json:"cluster"`
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
}

// ExamJSON is the exam clock in `astrona status -o json`.
type ExamJSON struct {
	StartedAt        time.Time `json:"startedAt"`
	LimitSeconds     int64     `json:"limitSeconds"`
	RemainingSeconds int64     `json:"remainingSeconds"`
	Over             bool      `json:"over"`
}

// ForwardJSON is a port forward in -o json output.
type ForwardJSON struct {
	Name      string `json:"name"`
	State     string `json:"state"`
	URL       string `json:"url"`
	Target    string `json:"target"`
	Namespace string `json:"namespace"`
	Cluster   string `json:"cluster,omitempty"`
	Restarts  int    `json:"restarts"`
	LastError string `json:"lastError,omitempty"`
}

// ForwardsJSON is fs as -o json prints them (never nil).
func ForwardsJSON(fs []portforward.Forward) []ForwardJSON {
	out := make([]ForwardJSON, 0, len(fs))
	for _, f := range fs {
		pf := f.Spec.Forward.Normalized()
		out = append(out, ForwardJSON{Name: pf.Name, State: string(f.Effective()), URL: portforward.LocalURL(pf),
			Target: fmt.Sprintf("%s:%d", pf.Resource, pf.TargetPort), Namespace: pf.Namespace, Cluster: pf.Cluster,
			Restarts: f.Status.Restarts, LastError: f.Status.LastError})
	}
	return out
}

// Result is st as a StatusResult.
func (st LabStatus) Result() StatusResult {
	out := StatusResult{Name: st.Row.Name, Runtime: st.Row.Runtime, Status: st.Row.Status, Uptime: st.Row.Uptime,
		PortForwards: ForwardsJSON(st.Forwards), LinkedClusters: []LinkedClusterJSON{}, Attempts: len(st.Attempts), Next: NextStep(st), Lab: st}
	if st.Row.Version != "-" {
		out.Kubernetes = st.Row.Version
	}
	if st.Row.Runtime == "kind" {
		out.Context = "kind-" + st.Row.Name
	}
	for _, l := range st.Links {
		status := "not running"
		if r, ok := st.LinkRows[l.Cluster]; ok {
			status = r.Status
		}
		out.LinkedClusters = append(out.LinkedClusters, LinkedClusterJSON{Name: l.Name, Cluster: l.Cluster, Status: status, Hostname: l.Hostname()})
	}
	if st.Exam != nil {
		out.Exam = &ExamJSON{StartedAt: st.Exam.StartedAt, LimitSeconds: int64(st.Exam.Limit.Seconds()),
			RemainingSeconds: int64(st.Exam.Remaining(st.Now).Seconds()), Over: st.Exam.Over(st.Now)}
	}
	if n := len(st.Attempts); n > 0 {
		out.LastAttempt = &st.Attempts[n-1]
	}
	return out
}

// NextStep suggests the one most useful command for where the student is.
func NextStep(st LabStatus) string {
	if st.Cfg != nil && st.Cfg.Metadata.Docs.Question != "" && len(st.Attempts) == 0 {
		return "read the task — astrona docs question"
	}
	if st.Exam != nil && st.Exam.Over(st.Now) {
		return "time is up — astrona submit to record your result, astrona reset to try again"
	}
	if n := len(st.Attempts); n > 0 && st.Attempts[n-1].Pass {
		return "passed — astrona reset to practice again, astrona destroy when done"
	}
	if strings.HasPrefix(st.Row.Status, "Stopped") {
		return "astrona start " + st.Row.Name
	}
	if n := len(st.Attempts); n > 0 {
		return "astrona submit --watch for live feedback while you fix it — or astrona reset --soft to start over"
	}
	return "astrona submit to grade your work"
}
