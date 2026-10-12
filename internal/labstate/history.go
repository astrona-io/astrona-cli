package labstate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/config"
)

// FinishedRun records a completed local run without credentials or session IDs.
type FinishedRun struct {
	Lab        string    `json:"lab"`
	Kind       string    `json:"kind"`
	Catalog    string    `json:"catalog,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

func runHistoryDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, apphome.Name(), "results", "runs"), nil
}

// RecordFinishedRun preserves one record per readiness timestamp. Linked
// clusters, temporary authoring tests and older runs without a start time are
// omitted. The existing results directory is protected from cache cleanup.
func RecordFinishedRun(lab string, st *State, at time.Time) error {
	if err := config.ValidateName(lab); err != nil {
		return err
	}
	if st == nil || st.Source == nil || st.StartedAt == "" || strings.HasPrefix(lab, apphome.TestClusterPrefix()) {
		return nil
	}
	started, err := time.Parse(time.RFC3339, st.StartedAt)
	if err != nil {
		return fmt.Errorf("run history start time: %w", err)
	}
	kind := "Lab"
	if st.Session.IsPlayground() || strings.Contains(st.Source.Catalog, "/playground") || filepath.Base(st.Source.Config) == "playground" {
		kind = "Playground"
	}
	dir, err := runHistoryDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	id := sha256.Sum256([]byte(lab + "\n" + st.StartedAt))
	path := filepath.Join(dir, fmt.Sprintf("%x.json", id))
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(FinishedRun{Lab: lab, Kind: kind, Catalog: st.Source.Catalog, StartedAt: started, FinishedAt: at.UTC()})
	if err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'))
}

// FinishedRuns reads completed runs newest first. Invalid records are skipped;
// unreadable files are reported rather than silently hiding history.
func FinishedRuns() ([]FinishedRun, error) {
	dir, err := runHistoryDir()
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	d, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	var runs []FinishedRun
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		f, err := root.Open(entry.Name())
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		var run FinishedRun
		if len(data) <= 16*1024 && json.Unmarshal(data, &run) == nil && config.ValidateName(run.Lab) == nil && !run.FinishedAt.IsZero() {
			runs = append(runs, run)
		}
	}
	slices.SortFunc(runs, func(a, b FinishedRun) int { return b.FinishedAt.Compare(a.FinishedAt) })
	return runs, nil
}
