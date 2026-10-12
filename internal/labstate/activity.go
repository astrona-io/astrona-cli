package labstate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/config"
)

// ActivityPeriod is time spent in a lab UI, excluding confirmed idle periods.
type ActivityPeriod struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func activityDir(lab, started string) (string, error) {
	if err := config.ValidateName(lab); err != nil {
		return "", err
	}
	if _, err := time.Parse(time.RFC3339, started); err != nil {
		return "", fmt.Errorf("activity requires a lab readiness time: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	id := sha256.Sum256([]byte(lab + "\n" + started))
	return filepath.Join(home, apphome.Name(), "results", "activity", fmt.Sprintf("%x", id)), nil
}

// SaveActivity writes one UI's periods separately from lab lifecycle state.
// Independent files prevent concurrent UIs from overwriting each other.
func SaveActivity(lab, started, session string, periods []ActivityPeriod) error {
	if err := config.ValidateName(session); err != nil {
		return err
	}
	dir, err := activityDir(lab, started)
	if err != nil {
		return err
	}
	data, err := json.Marshal(periods)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, session+".json"), data)
}

// ActiveTime unions overlapping UI periods so two open terminals cannot
// double-count the same time. No data means the run was not tracked.
func ActiveTime(lab, started string) (time.Duration, bool, error) {
	if started == "" {
		return 0, false, nil
	}
	dir, err := activityDir(lab, started)
	if err != nil {
		return 0, false, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var periods []ActivityPeriod
	tracked := false
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return 0, false, err
		}
		var saved []ActivityPeriod
		if err := json.Unmarshal(data, &saved); err != nil {
			return 0, false, fmt.Errorf("read lab activity: %w", err)
		}
		periods = append(periods, saved...)
		tracked = true
	}
	return activityTotal(periods), tracked, nil
}

func activityTotal(periods []ActivityPeriod) time.Duration {
	sort.Slice(periods, func(i, j int) bool { return periods[i].Start.Before(periods[j].Start) })
	var end time.Time
	var total time.Duration
	for _, period := range periods {
		start := period.Start
		if start.Before(end) {
			start = end
		}
		if period.End.After(start) {
			total += period.End.Sub(start)
			end = period.End
		}
	}
	return total
}
