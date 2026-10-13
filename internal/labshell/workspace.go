// Package labshell runs student terminals without exposing the host filesystem.
package labshell

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"astrona/internal/apphome"
	"astrona/internal/config"
)

// openRuntime rejects symlinks at every managed directory boundary. The shell
// only mounts work/, so it cannot replace its own mount source or config/.
func openRuntime(create bool) (*os.Root, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, "", err
	}
	r, err := os.OpenRoot(home)
	if err != nil {
		return nil, "", err
	}
	path := home
	for _, name := range []string{apphome.Name(), "runtime"} {
		next, err := childDir(r, name, create)
		r.Close()
		if err != nil {
			return nil, "", err
		}
		r = next
		path = filepath.Join(path, name)
	}
	return r, path, nil
}

func childDir(parent *os.Root, name string, create bool) (*os.Root, error) {
	if create {
		if err := parent.Mkdir(name, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace %s must be a directory, not a symlink", name)
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		r.Close()
		return nil, fmt.Errorf("workspace directory changed while opening %s", name)
	}
	return r, nil
}

func prepareWorkspace(lab string) (*os.Root, string, error) {
	if err := config.ValidateName(lab); err != nil {
		return nil, "", err
	}
	r, path, err := openRuntime(true)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	root, err := childDir(r, lab, true)
	if err != nil {
		return nil, "", err
	}
	for _, name := range []string{"work", "config"} {
		dir, err := childDir(root, name, true)
		if err != nil {
			root.Close()
			return nil, "", err
		}
		dir.Close()
	}
	return root, filepath.Join(path, lab), nil
}

func writePrivate(root *os.Root, name string, data []byte) error {
	// Never truncate a symlink or hard link planted in a previous workspace.
	tmp := ".terminal-" + rand.Text()
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return root.Rename(tmp, name)
}
