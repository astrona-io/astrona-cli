package labstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"astrona/internal/config"
)

type activityPresence struct {
	Active  bool      `json:"active"`
	Updated time.Time `json:"updated"`
}

// SavePresence publishes one UI's activity without changing another UI's state.
func SavePresence(lab, started, session string, active bool, now time.Time) error {
	if err := config.ValidateName(session); err != nil {
		return err
	}
	dir, err := activityDir(lab, started)
	if err != nil {
		return err
	}
	dir = filepath.Join(dir, "presence")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(activityPresence{active, now})
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, session+".json"), data)
}

// HasActiveUI reports whether any UI has published an active heartbeat in the
// last 15 seconds. A crashed process cannot leave a run permanently active.
func HasActiveUI(lab, started string, now time.Time) (bool, error) {
	if started == "" {
		return false, nil
	}
	if finished, err := ReadSubmission(lab, started); err != nil || finished != nil {
		return false, err
	}
	dir, err := activityDir(lab, started)
	if err != nil {
		return false, err
	}
	dir = filepath.Join(dir, "presence")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return false, err
		}
		var presence activityPresence
		if err := json.Unmarshal(data, &presence); err != nil {
			return false, err
		}
		age := now.Sub(presence.Updated)
		if presence.Active && age >= 0 && age < 15*time.Second {
			return true, nil
		}
	}
	return false, nil
}
