package main

import (
	"fmt"
	"os"

	"astrona/internal/catalog"
	"astrona/internal/labstate"
)

// Match saved runs before catalog or Git resolution can update their files.
func savedSubmission(args []string, flags *rootFlags) (string, *labstate.State, error) {
	selected := *flags
	var source *labstate.Source
	if len(args) > 0 && !flags.gitExplicit {
		if _, err := os.Stat(args[0]); os.IsNotExist(err) && catalog.LooksLikeLabID(args[0]) {
			source = &labstate.Source{Catalog: args[0]}
		}
	}
	if source == nil {
		useLabArg(args, &selected)
		source = rememberedSource(&selected)
	}
	names, err := labstate.Names()
	if err != nil {
		return "", nil, err
	}
	var name string
	var found *labstate.State
	for _, candidate := range names {
		st, err := labstate.Load(candidate)
		if err != nil {
			return "", nil, err
		}
		if st == nil || !sameSource(st.Source, source) {
			continue
		}
		if found != nil {
			return "", nil, fmt.Errorf("several saved runs match this source; name its config with astrona submit -c <lab-dir>")
		}
		name, found = candidate, st
	}
	return name, found, nil
}
