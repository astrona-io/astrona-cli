package labops

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"astrona/internal/apphome"
	"astrona/internal/config"
	"astrona/internal/filelock"
)

// A hands-on exam (`astrona hands-on-exam`) runs X kind clusters prepared from the
// exam's cluster profile, with every drawn task's setup applied to the
// cluster labs assigned it. Labs keeps the attempt and its clock; this
// machine keeps only what it needs to find its clusters again, in
// ~/.astrona/exams/current.json. One attempt at a time: labs allows one
// open attempt per student.

// ExamCluster is one of the attempt's kind clusters: its number (1..X, the
// exam-<n> context) and its kind cluster name.
type ExamCluster struct {
	N    int    `json:"n"`
	Name string `json:"name"`
}

// ExamTaskRef is a drawn task as this machine needs it: where it runs and
// which catalog lab grades it.
type ExamTaskRef struct {
	Position int    `json:"position"`
	Cluster  int    `json:"cluster"`
	Lab      string `json:"lab"`
	Scope    string `json:"scope,omitempty"`
}

// ExamPendingResult is a graded task an older astrona kept in the local
// state to send later. They are moved to the exam queue (ExamSignal) when
// it is next flushed.
type ExamPendingResult struct {
	Position int             `json:"position"`
	Key      string          `json:"key"`
	Result   json.RawMessage `json:"result"`
	At       time.Time       `json:"at"`
}

// ExamState is ~/.astrona/exams/current.json.
type ExamState struct {
	ID      string          `json:"id"`
	Exam    string          `json:"exam"`
	Title   string          `json:"title,omitempty"`
	Site    string          `json:"site"`
	URL     string          `json:"url,omitempty"`
	Profile json.RawMessage `json:"profile"`

	Clusters []ExamCluster `json:"clusters"`
	Tasks    []ExamTaskRef `json:"tasks"`

	// The kubectl current-context before the exam, put back at teardown
	// when the student left it on an exam context.
	PreviousContext string `json:"previousContext,omitempty"`
	PreviousUnset   bool   `json:"previousUnset,omitempty"`

	Current     int  `json:"current,omitempty"` // the task last shown with `astrona hands-on-exam task`
	Ready       bool `json:"ready,omitempty"`   // every cluster built and every setup applied
	Paused      bool `json:"paused,omitempty"`  // the node containers are paused for a break
	WatchdogPID int  `json:"watchdogPid,omitempty"`
	// Ended: the clusters are gone; an older astrona kept the file only for
	// Pending. Today the file goes with the clusters.
	Ended bool `json:"ended,omitempty"`
	// Graded: this machine graded every task at the end (submit, deadline
	// or wake), so a wake after the deadline doesn't grade them again.
	Graded bool `json:"graded,omitempty"`
	// Notified is the countdown notice (minutes left: 60, 30, 15, 5, 1) the
	// watchdog last showed; 0 before the first.
	Notified int `json:"notified,omitempty"`
	// LastActivity is when activity was last reported to labs (at most once
	// a minute).
	LastActivity time.Time `json:"lastActivity,omitzero"`

	// Pending is an older astrona's unsent results (see ExamPendingResult).
	Pending   []ExamPendingResult `json:"pending,omitempty"`
	CreatedAt time.Time           `json:"createdAt"`
}

// ExamContext is the kubectl context of cluster n.
func ExamContext(n int) string { return "exam-" + strconv.Itoa(n) }

// ExamClusterName is the kind cluster n of attempt id:
// astro-exam-<first 8 of id>-<n>.
func ExamClusterName(id string, n int) string {
	short := strings.ReplaceAll(id, "-", "")
	if len(short) > 8 {
		short = short[:8]
	}
	return fmt.Sprintf("%sexam-%s-%d", apphome.ClusterPrefix(), strings.ToLower(short), n)
}

// IsExamCluster reports whether name is an exam cluster of any attempt.
func IsExamCluster(name string) bool {
	return strings.HasPrefix(name, apphome.ClusterPrefix()+"exam-")
}

// ClusterName is cluster n's kind cluster, or "".
func (s *ExamState) ClusterName(n int) string {
	for _, c := range s.Clusters {
		if c.N == n {
			return c.Name
		}
	}
	return ""
}

// Task returns the task at position.
func (s *ExamState) Task(position int) (ExamTaskRef, bool) {
	for _, t := range s.Tasks {
		if t.Position == position {
			return t, true
		}
	}
	return ExamTaskRef{}, false
}

// KindProfile is the attempt's cluster profile as a kind config.
func (s *ExamState) KindProfile() (*config.KindConfig, error) {
	return ParseClusterProfile(s.Profile)
}

// NewExamState is the local record of a freshly drawn attempt.
func NewExamState(site string, profile json.RawMessage, id, exam, title, url string, clusterCount int, tasks []ExamTaskRef, now time.Time) *ExamState {
	st := &ExamState{ID: id, Exam: exam, Title: title, Site: site, URL: url, Profile: profile, Tasks: tasks, CreatedAt: now.UTC()}
	for n := 1; n <= clusterCount; n++ {
		st.Clusters = append(st.Clusters, ExamCluster{N: n, Name: ExamClusterName(id, n)})
	}
	sort.Slice(st.Tasks, func(i, j int) bool { return st.Tasks[i].Position < st.Tasks[j].Position })
	return st
}

// ExamStatePath is ~/.astrona/exams/current.json.
func ExamStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}
	return filepath.Join(home, apphome.Name(), "exams", "current.json"), nil
}

// LoadExamState returns the local attempt, or nil when there is none.
func LoadExamState() (*ExamState, error) {
	path, err := ExamStatePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read exam state: %w", err)
	}
	var s ExamState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &s, nil
}

// SaveExamState writes the local attempt (0600).
func SaveExamState(s *ExamState) error {
	return UpdateExamState(func(cur *ExamState) (*ExamState, error) { return s, nil })
}

// UpdateExamState applies change to the local attempt under a lock shared
// by the exam commands and the watchdog. change gets nil when there is no
// attempt; returning nil removes the file.
func UpdateExamState(change func(*ExamState) (*ExamState, error)) error {
	path, err := ExamStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	release, _, err := filelock.Lock(path+".lock", true)
	if err != nil {
		return err
	}
	defer release()
	cur, err := LoadExamState()
	if err != nil {
		return err
	}
	next, err := change(cur)
	if err != nil {
		return err
	}
	if next == nil {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove exam state: %w", err)
		}
		return nil
	}
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	return writeExamFile(path, append(data, '\n'))
}

// RemoveExamState forgets the local attempt.
func RemoveExamState() error {
	return UpdateExamState(func(*ExamState) (*ExamState, error) { return nil, nil })
}

func writeExamFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// ParseClusterProfile reads an exam's cluster profile, the `kind` block of
// its lab_exam.clusters, as content-sync sends it. Besides a lab's own kind
// config it takes kind's node list (nodes: [{role: control-plane},
// {role: worker}]) and gatewayAPI: true (the envoy gateway). The gateway's
// host ports are never mapped: X clusters can't share them.
func ParseClusterProfile(raw json.RawMessage) (*config.KindConfig, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("the exam has no cluster profile")
	}
	var generic map[string]any
	if err := yaml.Unmarshal(raw, &generic); err != nil {
		return nil, fmt.Errorf("exam cluster profile: %w", err)
	}
	if list, ok := generic["nodes"].([]any); ok {
		nodes := map[string]any{"controlPlanes": 0, "workers": 0}
		cps, workers := 0, 0
		for _, n := range list {
			m, _ := n.(map[string]any)
			switch m["role"] {
			case "control-plane", nil:
				cps++
			case "worker":
				workers++
			default:
				return nil, fmt.Errorf("exam cluster profile: unknown node role %v", m["role"])
			}
		}
		nodes["controlPlanes"], nodes["workers"] = cps, workers
		generic["nodes"] = nodes
	}
	if addons, ok := generic["addons"].(map[string]any); ok {
		switch v := addons["gatewayAPI"].(type) {
		case bool:
			if v {
				addons["gatewayAPI"] = "envoy"
			} else {
				delete(addons, "gatewayAPI")
			}
		}
	}
	data, err := yaml.Marshal(generic)
	if err != nil {
		return nil, err
	}
	var k config.KindConfig
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&k); err != nil {
		return nil, fmt.Errorf("exam cluster profile: %w", err)
	}
	if len(k.Clusters) > 0 || len(k.Labs) > 0 {
		return nil, errors.New("exam cluster profile: linked clusters are not supported")
	}
	k.Addons.SkipHostPorts = true
	if err := config.ValidateKindConfig(config.RuntimeConfig{Type: "kind", Kind: &k}); err != nil {
		return nil, fmt.Errorf("exam cluster profile: %w", err)
	}
	return &k, nil
}

// examRuntime is the runtime every cluster of the attempt is built with.
func examRuntime(profile *config.KindConfig) config.RuntimeConfig {
	k := *profile
	return config.RuntimeConfig{Type: "kind", Kind: &k}
}
