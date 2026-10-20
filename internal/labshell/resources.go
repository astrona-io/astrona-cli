package labshell

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"astrona/internal/config"
	"astrona/internal/resources"
)

// Resources are seeded once per workspace. Full run/reset removes the old
// workspace before snapshotting resources; opening more tabs never overwrites
// edits. Metadata and the helper stay outside the student's writable mount.
func prepareResources(root *os.Root, path, lab string) error {
	if _, err := root.Lstat("config/resources-ready"); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := root.Lstat("work/resources"); err == nil {
		return fmt.Errorf("workspace resources already exist without a receipt; move them aside before opening this lab")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stage := "config/.resources-" + rand.Text()
	defer root.RemoveAll(stage)
	list, err := resources.CopySnapshot(lab, filepath.Join(path, filepath.FromSlash(stage)))
	if err != nil {
		return err
	}
	helper, err := resources.ShellHelper(list)
	if err != nil {
		return err
	}
	bin, err := childDir(root, "config", false)
	if err != nil {
		return err
	}
	defer bin.Close()
	helperDir, err := childDir(bin, "bin", true)
	if err != nil {
		return err
	}
	defer helperDir.Close()
	if err := writePrivate(helperDir, "astrona", helper); err != nil {
		return err
	}
	if err := helperDir.Chmod("astrona", 0o700); err != nil {
		return err
	}
	if err := writePrivate(root, "config/bashrc", []byte(`_astrona_resources_complete() {
  COMPREPLY=()
  local item current="${COMP_WORDS[COMP_CWORD]}"
  if [[ "${COMP_WORDS[1]:-}" =~ ^(res|resource|resources)$ ]] && (( COMP_CWORD == 3 )); then
    while IFS= read -r item; do
      [[ "$item" == "$current"* ]] && COMPREPLY+=("$item")
    done < <(astrona res _complete)
  fi
}
complete -F _astrona_resources_complete astrona
`)); err != nil {
		return err
	}
	if err := root.Rename(stage, "work/resources"); err != nil {
		return err
	}
	return writePrivate(root, "config/resources-ready", []byte("1\n"))
}

// ResourceDir returns the editable host directory for an initialized Kind
// workspace. It does not create one or follow a replaced directory boundary.
func ResourceDir(lab string) (string, error) {
	if err := config.ValidateName(lab); err != nil {
		return "", err
	}
	runtime, path, err := openRuntime(false)
	if err != nil {
		return "", err
	}
	defer runtime.Close()
	root, err := childDir(runtime, lab, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	work, err := childDir(root, "work", false)
	if err != nil {
		return "", err
	}
	defer work.Close()
	dir, err := childDir(work, "resources", false)
	if err != nil {
		return "", err
	}
	dir.Close()
	return filepath.Join(path, lab, "work", "resources"), nil
}
