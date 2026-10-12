package labstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ResourceUsage holds container totals and sample means for an observed run.
// These local measurements are not part of account submissions.
type ResourceUsage struct {
	CPU           float64   `json:"cpu"`
	Memory        float64   `json:"memory"`
	AverageCPU    float64   `json:"averageCpu"`
	AverageMemory float64   `json:"averageMemory"`
	Samples       uint64    `json:"samples"`
	Updated       time.Time `json:"updated"`
}

// RecordUsage accepts at most one sample per five seconds across all UIs.
// Missing samples do not count as zero or contribute unobserved time.
func RecordUsage(lab, started string, cpu, memory float64, now time.Time) (ResourceUsage, error) {
	var usage ResourceUsage
	if cpu < 0 || memory < 0 || math.IsNaN(cpu) || math.IsNaN(memory) || math.IsInf(cpu, 0) || math.IsInf(memory, 0) {
		return usage, fmt.Errorf("invalid resource usage")
	}
	dir, err := activityDir(lab, started)
	if err != nil {
		return usage, err
	}
	dir = filepath.Join(dir, "resources")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return usage, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "usage.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return usage, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return loadUsage(filepath.Join(dir, "usage.json"))
		}
		return usage, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	path := filepath.Join(dir, "usage.json")
	usage, err = loadUsage(path)
	if err != nil {
		return usage, err
	}
	if !usage.Updated.IsZero() && now.Sub(usage.Updated) < 5*time.Second {
		return usage, nil
	}
	usage.Samples++
	usage.CPU, usage.Memory, usage.Updated = cpu, memory, now
	usage.AverageCPU += (cpu - usage.AverageCPU) / float64(usage.Samples)
	usage.AverageMemory += (memory - usage.AverageMemory) / float64(usage.Samples)
	data, err := json.Marshal(usage)
	if err != nil {
		return usage, err
	}
	return usage, writeFileAtomic(path, data)
}

func loadUsage(path string) (ResourceUsage, error) {
	var usage ResourceUsage
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return usage, nil
	}
	if err != nil {
		return usage, err
	}
	err = json.Unmarshal(data, &usage)
	return usage, err
}
