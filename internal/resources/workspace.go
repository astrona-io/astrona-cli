package resources

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

func WorkspaceID(lab string) (string, error) {
	list, dir, err := Load(lab)
	if err != nil {
		return "", err
	}
	metadata, err := json.Marshal(list)
	if err != nil {
		return "", err
	}
	var generation []byte
	root, err := openNoLink(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if root != nil {
		defer root.Close()
		file, err := root.Open(".generation")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if file != nil {
			defer file.Close()
			generation, err = io.ReadAll(io.LimitReader(file, 128))
			if err != nil {
				return "", err
			}
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256(append(metadata, generation...))), nil
}

// WorkspaceBundle includes all resources and the small in-lab CLI. The ID
// changes on full reset, but not when a terminal is reopened or the CLI changes.
func WorkspaceBundle(lab string) (string, string, func(), error) {
	tmp, err := os.MkdirTemp("", "astrona-resources-")
	if err != nil {
		return "", "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	fail := func(err error) (string, string, func(), error) { cleanup(); return "", "", nil, err }
	bundle := filepath.Join(tmp, "workspace")
	if err := os.Mkdir(bundle, 0o700); err != nil {
		return fail(err)
	}
	list, err := CopySnapshot(lab, filepath.Join(bundle, "resources"))
	if err != nil {
		return fail(err)
	}
	helper, err := ShellHelper(list)
	if err != nil {
		return fail(err)
	}
	id, err := WorkspaceID(lab)
	if err != nil {
		return fail(err)
	}
	if err := os.Mkdir(filepath.Join(bundle, "bin"), 0o700); err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "bin", "astrona"), helper, 0o700); err != nil {
		return fail(err)
	}
	return bundle, id, cleanup, nil
}

// CopySnapshot copies approved resources into a new private staging directory.
// It never reads through symlinks or writes into a student's existing files.
func CopySnapshot(lab, dest string) ([]Resource, error) {
	list, dir, err := Load(lab)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(dest, 0o700); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return list, nil
	}
	root, err := openNoLink(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	budget := copyBudget{dirMode: 0o700, fileMode: 0o600, execMode: 0o700}
	for _, r := range list {
		if _, err := r.Path(dest); err != nil {
			return nil, err
		}
		target := filepath.Join(dest, filepath.FromSlash(r.File))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return nil, err
		}
		if err := budget.copyTree(root, r.File, target); err != nil {
			return nil, fmt.Errorf("copy resource %s: %w", r.File, err)
		}
	}
	return list, nil
}

func quoteShell(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func resourceDisplay(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\ufffd' {
			return ' '
		}
		return r
	}, s)
}

// ShellHelper is a resource-only Astrona command for a container or VM. It
// needs just POSIX sh, not a host binary, control socket, credentials or eval.
// Resource metadata becomes quoted literals; student arguments remain "$@".
func ShellHelper(list []Resource) ([]byte, error) {
	var out strings.Builder
	out.WriteString(`#!/bin/sh
set -eu
case "${1:-}" in res|resource|resources) shift ;; *) echo 'This lab terminal supports astrona res: list, show, path, copy, run.' >&2; exit 2 ;; esac
root=${ASTRONA_RESOURCE_DIR:-"$HOME/resources"}
action=${1:-list}
if [ "$#" -gt 0 ]; then shift; fi
case "$action" in list|--help|-h)
printf '%s\n' "Resources for ${ASTRONA_LAB:-this lab}: $root"
`)
	// Descriptions are display text, never executable shell fragments.
	for _, r := range list {
		fmt.Fprintf(&out, "printf '  %%s\\n' %s\n", quoteShell(resourceDisplay(r.File+"  "+r.Description)))
	}
	out.WriteString("exit 0 ;; _complete)\n")
	for _, r := range list {
		fmt.Fprintf(&out, "printf '%%s\\n' %s\n", quoteShell(r.File))
	}
	out.WriteString("exit 0 ;; esac\nname=${1:-}\nif [ -z \"$name\" ]; then echo 'A resource name is required.' >&2; exit 2; fi\nshift\ncase \"$name\" in\n")
	for _, r := range list {
		if _, err := r.Path("/resources"); err != nil {
			return nil, err
		}
		if strings.ContainsFunc(r.File+r.Name, unicode.IsControl) {
			return nil, fmt.Errorf("resource filenames must not contain control characters")
		}
		fmt.Fprintf(&out, "%s|%s) file=%s; how=%s; runner() {\n", quoteShell(r.Name), quoteShell(r.File), quoteShell(r.File), quoteShell(r.How))
		switch r.How {
		case HowBash:
			out.WriteString("exec bash \"$file\" \"$@\"\n")
		case HowApply:
			out.WriteString("exec kubectl apply -f \"$file\" \"$@\"\n")
		case HowExec:
			out.WriteString("exec \"$file\" \"$@\"\n")
		case HowCommand:
			fields := strings.Fields(r.Run)
			if len(fields) == 0 {
				return nil, fmt.Errorf("resource %s has an empty run command", r.Name)
			}
			out.WriteString("exec ")
			for _, arg := range fields {
				out.WriteString(quoteShell(arg) + " ")
			}
			out.WriteString("\"$@\"\n")
		default:
			out.WriteString("echo 'This resource is a file; use astrona res show, copy or path.' >&2; exit 2\n")
		}
		out.WriteString("} ;;\n")
	}
	out.WriteString(`*) printf 'Unknown resource: %s. Use astrona res to list resources.\n' "$name" >&2; exit 2 ;; esac
file="$root/$file"
case "$action" in
path) printf '%s\n' "$file" ;;
show) if [ -d "$file" ]; then find "$file" -type f; else cat "$file"; fi ;;
copy) dest=${1:-.}; if [ -d "$dest" ]; then dest="$dest/$(basename "$file")"; fi; if [ -e "$dest" ] || [ -L "$dest" ]; then echo 'Destination exists; choose another path.' >&2; exit 1; fi; cp -R "$file" "$dest" ;;
run)
if [ "${1:-}" = -- ]; then shift; fi
export ASTRONA_RESOURCE_DIR="$root"
if [ -d "$file" ]; then cd "$file"; else cd "$(dirname "$file")"; fi
printf 'Running %s in lab %s (%s), directory %s\n' "$name" "${ASTRONA_LAB:-unknown}" "${ASTRONA_RESOURCE_TARGET:-terminal}" "$PWD" >&2
runner "$@" ;;
*) echo 'Use astrona res list, show, path, copy or run.' >&2; exit 2 ;;
esac
`)
	return []byte(out.String()), nil
}
