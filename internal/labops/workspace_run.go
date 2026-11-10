package labops

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"astrona/internal/apphome"
	"astrona/internal/config"
	"astrona/internal/ui"
)

// WorkspaceRunDir is where the browser workspace keeps one lab's run:
// run.json (the run record), start.json (the Start click) and
// submission.json (the graded result the page shows), not created here.
func WorkspaceRunDir(lab string) (string, error) {
	if err := config.ValidateName(lab); err != nil {
		return "", fmt.Errorf("lab name: %w", err)
	}
	dir, err := apphome.WorkspaceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runs", lab), nil
}

// clearWorkspaceStart removes the lab's Start marker once the lab is gone:
// its time belongs to that run only. The run record and submission stay,
// so an open workspace page still shows the run as stopped with its result.
func clearWorkspaceStart(w io.Writer, lab string) {
	dir, err := WorkspaceRunDir(lab)
	if err != nil {
		ui.Fwarnf(w, "could not clear the workspace start of %s: %s", lab, err)
		return
	}
	if err := os.Remove(filepath.Join(dir, "start.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		ui.Fwarnf(w, "could not clear the workspace start of %s: %s", lab, err)
	}
}

// clearWorkspaceRun removes what the browser workspace kept about an
// earlier run of the lab, so a new run never inherits its record, Start
// time or result. A browser run writes its own record once it is ready.
func clearWorkspaceRun(lab string) error {
	dir, err := WorkspaceRunDir(lab)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
