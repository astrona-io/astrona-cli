package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// maxManifestDepth bounds how far FindManifest looks above a lab's
// directory (sections/section-010/module-01/labs/lab-02 is five levels).
const maxManifestDepth = 8

// maxManifestBytes bounds the astrona.yaml FindManifest reads.
const maxManifestBytes = 2 << 20

// FindManifest finds the training manifest (astrona.yaml) a local lab
// belongs to by walking up from labDir to the repository root (a folder
// with .git). It returns the manifest and the lab's path relative to it.
// A lab outside a training returns an error that is os.ErrNotExist.
func FindManifest(labDir string) (data []byte, labPath string, err error) {
	labDir, err = filepath.Abs(labDir)
	if err != nil {
		return nil, "", err
	}
	dir := labDir
	for range maxManifestDepth {
		data, err := readManifest(filepath.Join(dir, "astrona.yaml"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, "", err
		}
		if err == nil {
			rel, err := filepath.Rel(dir, labDir)
			if err != nil {
				return nil, "", err
			}
			return data, filepath.ToSlash(rel), nil
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break // the repository root, without a manifest
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, "", fmt.Errorf("no astrona.yaml above %s: %w", labDir, os.ErrNotExist)
}

// readManifest reads a training's astrona.yaml, a regular file of at most
// maxManifestBytes.
func readManifest(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxManifestBytes {
		return nil, fmt.Errorf("%s is not a regular file of at most %d KiB", path, maxManifestBytes>>10)
	}
	return os.ReadFile(path)
}
