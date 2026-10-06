package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// namePattern is what a lab name (metadata.name), qemu VM name
// (runtime.qemu[].name) or linked cluster name may look like. They end up
// as path components under ~/.astrona (state dirs destroy later removes)
// and inside qemu command-line options, so: no separators, no commas, no
// "..".
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// NameRule describes namePattern, for error messages.
const NameRule = "must start with a letter or digit and contain only letters, digits, '.', '_' or '-' (and no '..')"

// ValidateName rejects a name that isn't safe as a single path component
// under astrona's state dirs (see namePattern).
func ValidateName(name string) error {
	if !namePattern.MatchString(name) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid name '%s': %s", name, NameRule)
	}
	return nil
}

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

// JoinStrictlyWithinBaseDir is JoinWithinBaseDir without the absolute-path
// pass-through: source must be relative and stay inside baseDir. For paths
// that only ever make sense inside baseDir (lab docs, content bundle
// entries), where an absolute path is never a legitimate choice.
func JoinStrictlyWithinBaseDir(baseDir, source string) (string, error) {
	if filepath.IsAbs(source) {
		return "", fmt.Errorf("path '%s' must be relative, not absolute", source)
	}
	return JoinWithinBaseDir(baseDir, source)
}
