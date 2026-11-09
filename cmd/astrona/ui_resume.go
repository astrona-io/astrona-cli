package main

import (
	"fmt"

	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/labstate"
)

// Read the saved source locally. The normal run loader may fetch,
// reset the checkout or install another CLI, so it must not be used here
// (labops.LoadSavedLab). flags are pointed at the saved source, as the
// commands that follow (trust, submit) read it from them.
func loadSavedLab(name string, st *labstate.State, flags *rootFlags) (*config.LabConfig, string, func(), error) {
	noop := func() {}
	if st == nil || st.Source == nil {
		return nil, "", noop, fmt.Errorf("run %s has no saved config source", name)
	}
	applySource(flags, st.Source)
	lab, err := labops.LoadSavedLab(name, labOpsSource(flags), labops.LoadOptions{Version: Version})
	if err != nil {
		return nil, "", noop, err
	}
	return lab.Config, lab.BaseDir, lab.Cleanup, nil
}
