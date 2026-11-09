package labops

import (
	"encoding/json"
	"fmt"
	"time"

	"astrona/internal/labstate"
)

// RunID identifies one run of a lab from its saved state: a restart, a new
// session or another config makes a different ID. "" for no state.
func RunID(st *labstate.State) string {
	if st == nil {
		return ""
	}
	session := ""
	if st.Session != nil {
		session = st.Session.ID
	}
	source, _ := json.Marshal(st.Source) // Source contains only strings.
	return st.StartedAt + "\n" + session + "\n" + string(source)
}

// GuardRun fails when the lab's saved run is no longer runID (stopped or
// restarted elsewhere) or its playground time limit has passed, so an
// action chosen against one run never reaches another.
func GuardRun(name, runID string) error {
	st, err := labstate.Load(name)
	if err != nil {
		return err
	}
	if RunID(st) != runID {
		return fmt.Errorf("lab stopped or restarted elsewhere; close and reopen the workspace")
	}
	if st != nil && st.Session.IsPlayground() {
		if deadline, err := time.Parse(time.RFC3339, st.Session.DeadlineAt); err == nil && !time.Now().Before(deadline) {
			return fmt.Errorf("playground time limit reached; its watchdog handles cleanup")
		}
	}
	return nil
}
