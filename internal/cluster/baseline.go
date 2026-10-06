package cluster

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// Baseline is a cluster's platform namespaces — what existed right before
// the lab's bootstrap (kube-system, addons, …). `astrona reset --soft`
// keeps those and deletes every namespace created after them. Saved in
// the cluster's state dir, so it goes away with the cluster.
type Baseline struct {
	Namespaces []string `json:"namespaces"`
}

func baselinePath(lab string) (string, error) {
	dir, err := labStateDir(lab)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "baseline.json"), nil
}

// WriteBaseline saves lab's baseline.
func WriteBaseline(lab string, b Baseline) error {
	path, err := baselinePath(lab)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0600)
}

// ReadBaseline returns lab's baseline; ok is false when it has none (a
// lab created before baselines existed).
func ReadBaseline(lab string) (Baseline, bool, error) {
	path, err := baselinePath(lab)
	if err != nil {
		return Baseline{}, false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Baseline{}, false, nil
	}
	if err != nil {
		return Baseline{}, false, err
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return Baseline{}, false, err
	}
	return b, true, nil
}
