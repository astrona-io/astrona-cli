package main

import (
	"context"
	"io"
	"os"
	"path/filepath"

	"astrona/internal/labops"
	"astrona/internal/labstate"
)

// labConfigHere reports whether flags' config location (the current
// directory by default) holds a lab config.
func labConfigHere(flags *rootFlags) bool {
	if flags.gitURL != "" {
		return true
	}
	_, err := os.Stat(filepath.Join(flags.configPath, flags.fileName))
	return err == nil
}

const signInHint = labops.SignInHint

// sendResult sends a graded result (the object `astrona submit -o json`
// prints) to the lab page of the session the lab was started under, and
// reports whether it was recorded there (labops.SendResult). Every outcome
// is a line on w and warnings go to stderr — never stdout, so `submit -o
// json` stays clean; none of them changes the exit code. Check texts are
// capped as they go to the lab page.
func sendResult(ctx context.Context, w io.Writer, clusterName string, src *labstate.Source, result submissionResult) bool {
	return labops.SendResult(ctx, w, os.Stderr, clusterName, src, labops.CapCheckTexts(result))
}

// sameSource reports whether the config being graded (b) is the one the
// lab was started from (a, remembered by run).
func sameSource(a, b *labstate.Source) bool { return labops.SameSource(a, b) }
