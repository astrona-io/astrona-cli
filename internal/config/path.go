package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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

// JoinWithinBaseDir joins a relative source path onto baseDir and rejects
// anything that could reach a file outside baseDir. Lab configs come from
// untrusted git repos and URLs, so every path they name is
// attacker-influenced:
//
//   - an absolute path is refused (it names a host file, not a lab file);
//   - a path that leaves baseDir lexically ("../..") is refused;
//   - a path that leaves baseDir once symlinks are resolved (a link inside
//     the lab pointing at ~/.ssh) is refused. Both baseDir and the result
//     are resolved; for a path that doesn't exist yet, its deepest existing
//     ancestor is resolved instead, so a linked parent folder is caught too.
//
// The returned path is the lexical join (absolute), so messages show the
// path the author wrote. There is deliberately no variant that lets
// absolute paths through.
func JoinWithinBaseDir(baseDir, source string) (string, error) {
	if filepath.IsAbs(source) {
		return "", fmt.Errorf("path '%s' must be relative to the lab directory, not absolute — move the file into the lab and reference it by a relative path", source)
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory '%s': %w", baseDir, err)
	}

	joined := filepath.Join(absBase, source)
	if !within(absBase, joined) {
		return "", fmt.Errorf("path '%s' escapes lab directory '%s'", source, absBase)
	}

	realBase, err := resolveExisting(absBase)
	if err != nil {
		return "", fmt.Errorf("failed to resolve base directory '%s': %w", baseDir, err)
	}
	realJoined, err := resolveExisting(joined)
	if err != nil {
		return "", fmt.Errorf("failed to resolve path '%s': %w", source, err)
	}
	if !within(realBase, realJoined) {
		return "", fmt.Errorf("path '%s' escapes lab directory '%s' through a symlink (it resolves to '%s') — replace the link with the file itself", source, absBase, realJoined)
	}

	return joined, nil
}

// within reports whether path is base itself or lies below it. Both must
// be clean, absolute paths.
func within(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveExisting resolves symlinks in p. When p doesn't exist, the deepest
// existing ancestor is resolved and the missing remainder appended, so a
// symlinked parent folder is still accounted for.
func resolveExisting(p string) (string, error) {
	var rest []string
	cur := p
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(rest) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, rest[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		// A dangling link can't be resolved, and must not be treated as a
		// plain missing path: its target may lie anywhere.
		if fi, lerr := os.Lstat(cur); lerr == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("'%s' is a symlink whose target does not exist", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		rest = append(rest, filepath.Base(cur))
		cur = parent
	}
}
