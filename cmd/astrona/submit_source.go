package main

import (
	"os"

	"astrona/internal/catalog"
	"astrona/internal/labops"
	"astrona/internal/labstate"
)

// savedSubmission finds the saved run args and flags name (labops.FindSavedRun),
// matched before catalog or Git resolution can update their files.
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
	return labops.FindSavedRun(source)
}
