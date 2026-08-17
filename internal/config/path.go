package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// JoinWithinBaseDir resolves a possibly-relative source path against
// baseDir and rejects any result that escapes baseDir (e.g. via "../..").
// baseDir's own config can come from an untrusted remote URL
// (LoadLabConfig), so a relative source path is attacker-influenced and
// must not be allowed to reach files outside the lab directory. Absolute
// paths are passed through unchanged — that's an explicit, visible choice
// in the config, not a traversal.
func JoinWithinBaseDir(baseDir, source string) (string, error) {
	if filepath.IsAbs(source) {
		return source, nil
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory '%s': %w", baseDir, err)
	}

	joined := filepath.Join(absBase, source)

	rel, err := filepath.Rel(absBase, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path '%s' escapes lab directory '%s'", source, absBase)
	}

	return joined, nil
}
