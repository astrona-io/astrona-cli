// Package resources collects a lab's resources — the files and folders in
// its resources/ folder, described by `resources:` in config.yaml — and
// keeps a copy of them per running lab under ~/.astrona/resources/<lab>, so
// a student can show, copy or run them with `astrona resource` instead of
// copying commands out of the lab's docs.
//
// Nothing here runs a resource. Collecting refuses anything that would
// read outside the lab's resources/ folder (absolute paths, "..", symlinks)
// and caps the size, since a lab can come from any git repository.
package resources

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"astrona/internal/config"
)

// How a resource is used by `astrona resource run`.
const (
	HowFile    = "file"          // only shown or copied
	HowBash    = "bash"          // bash <file>
	HowApply   = "kubectl apply" // kubectl apply -f <file>, against the lab
	HowExec    = "exec"          // the file itself (an executable with #!)
	HowCommand = "command"       // the entry's run command, in its folder
)

// Limits on what a lab may ship as resources.
const (
	maxFiles     = 1000
	maxFileBytes = 50 << 20
	maxTotal     = 200 << 20
)

// manifestFile is the description of a lab's resources kept next to its
// copy, so `astrona resource` needs neither the config nor the lab folder.
const manifestFile = ".astrona-resources.json"

// Resource is one resource of a lab.
type Resource struct {
	Name        string `json:"name"` // what `astrona resource` calls it: the file name without extension
	File        string `json:"file"` // path inside resources/, slash-separated
	Description string `json:"description,omitempty"`
	How         string `json:"how"`           // one of the How… constants
	Run         string `json:"run,omitempty"` // HowCommand's command
	VM          string `json:"vm,omitempty"`  // qemu: the VM it runs in
	Dir         bool   `json:"dir,omitempty"` // a folder rather than a file
}

// Command is how the resource runs, for display: "bash setup.sh",
// "kubectl apply -f app.yaml", "go run ." — "" for a file.
func (r Resource) Command() string {
	switch r.How {
	case HowBash:
		return "bash " + path.Base(r.File)
	case HowApply:
		return "kubectl apply -f " + path.Base(r.File)
	case HowExec:
		return "./" + path.Base(r.File)
	case HowCommand:
		return r.Run
	}
	return ""
}

// Collect lists the lab's resources: every `resources:` entry, plus each
// file or folder directly in resources/ that no entry covers. baseDir is
// the folder holding config.yaml ("" for a config fetched from a URL, which
// has no folder). A lab without resources/ and without entries has none.
func Collect(cfg *config.LabConfig, baseDir string) ([]Resource, error) {
	if baseDir == "" {
		if len(cfg.Resources) > 0 {
			return nil, fmt.Errorf("resources need the lab's folder (a local lab or --git), not a config fetched from a URL")
		}
		return nil, nil
	}
	root, err := config.JoinStrictlyWithinBaseDir(baseDir, config.ResourcesDir)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(root)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if len(cfg.Resources) > 0 {
			return nil, fmt.Errorf("resources: listed in config.yaml, but the lab has no %s/ folder", config.ResourcesDir)
		}
		return nil, nil
	case err != nil:
		return nil, err
	case fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir():
		return nil, fmt.Errorf("%s/ must be a folder (not a link)", config.ResourcesDir)
	}

	qemu := cfg.Runtime.Type == "qemu"
	var out []Resource
	covered := map[string]bool{} // top-level names an entry covers
	for i, e := range cfg.Resources {
		file := path.Clean(strings.TrimSpace(e.File))
		full, err := config.JoinStrictlyWithinBaseDir(root, filepath.FromSlash(file))
		if err != nil {
			return nil, fmt.Errorf("resources[%d]: %w", i, err)
		}
		fi, err := os.Lstat(full)
		if err != nil {
			return nil, fmt.Errorf("resources[%d]: %s/%s doesn't exist", i, config.ResourcesDir, file)
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("resources[%d]: %s/%s is a link — resources must be real files", i, config.ResourcesDir, file)
		}
		r := Resource{File: file, Description: strings.TrimSpace(e.Description), VM: e.VM, Dir: fi.IsDir()}
		r.Name = nameOf(file)
		switch {
		case e.Type == config.ResourceTypeFile:
			r.How = HowFile
		case strings.TrimSpace(e.Run) != "":
			r.How, r.Run = HowCommand, strings.TrimSpace(e.Run)
		default:
			r.How = howOf(full, fi, qemu)
		}
		out = append(out, r)
		top, _, _ := strings.Cut(file, "/")
		covered[strings.ToLower(top)] = true
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, de := range entries {
		if strings.HasPrefix(de.Name(), ".") || covered[strings.ToLower(de.Name())] {
			continue
		}
		fi, err := os.Lstat(filepath.Join(root, de.Name()))
		if err != nil {
			return nil, err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s/%s is a link — resources must be real files", config.ResourcesDir, de.Name())
		}
		out = append(out, Resource{
			Name: nameOf(de.Name()), File: de.Name(), Dir: fi.IsDir(),
			How: howOf(filepath.Join(root, de.Name()), fi, qemu),
		})
	}

	seen := map[string]string{}
	for _, r := range out {
		if err := config.ValidateName(r.Name); err != nil {
			return nil, fmt.Errorf("%s/%s: rename it — a resource's name may only use letters, digits, '.', '_' and '-'", config.ResourcesDir, r.File)
		}
		if other, dup := seen[strings.ToLower(r.Name)]; dup {
			return nil, fmt.Errorf("%s/%s and %s/%s are both called %q — rename one", config.ResourcesDir, other, config.ResourcesDir, r.File, r.Name)
		}
		seen[strings.ToLower(r.Name)] = r.File
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// nameOf is a resource's name: its file name without the extension
// ("setup-db.sh" → "setup-db"); a folder keeps its whole name.
func nameOf(file string) string {
	base := path.Base(file)
	if ext := path.Ext(base); ext != "" && ext != base {
		return strings.TrimSuffix(base, ext)
	}
	return base
}

// howOf picks how a resource without a run command or type is used.
func howOf(full string, fi fs.FileInfo, qemu bool) string {
	if fi.IsDir() {
		return HowFile
	}
	switch strings.ToLower(filepath.Ext(full)) {
	case ".sh":
		return HowBash
	case ".yaml", ".yml":
		if qemu {
			return HowFile // no cluster to apply it to
		}
		return HowApply
	}
	if fi.Mode()&0o111 != 0 && hasShebang(full) {
		return HowExec
	}
	return HowFile
}

func hasShebang(file string) bool {
	f, err := os.Open(file)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 2)
	n, _ := io.ReadFull(f, b)
	return n == 2 && bytes.Equal(b, []byte("#!"))
}

// Dir is where the copy of clusterName's resources lives:
// ~/.astrona/resources/<clusterName>.
func Dir(clusterName string) (string, error) {
	if err := config.ValidateName(clusterName); err != nil {
		return "", fmt.Errorf("lab: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve home directory: %w", err)
	}
	return filepath.Join(home, ".astrona", "resources", clusterName), nil
}

// Snapshot replaces clusterName's copy with the lab's current resources
// (from baseDir/resources/) and returns where it is. With no resources,
// any old copy is removed and "" is returned.
func Snapshot(clusterName, baseDir string, list []Resource) (string, error) {
	dest, err := Dir(clusterName)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", Remove(clusterName)
	}
	root := filepath.Join(baseDir, config.ResourcesDir)
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), "."+clusterName+"-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp) // gone after the rename; cleans up on error

	var budget copyBudget
	for _, r := range list {
		src := filepath.Join(root, filepath.FromSlash(r.File))
		dst := filepath.Join(tmp, filepath.FromSlash(r.File))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", err
		}
		if err := budget.copyTree(src, dst); err != nil {
			return "", fmt.Errorf("%s/%s: %w", config.ResourcesDir, r.File, err)
		}
	}
	m, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, manifestFile), m, 0o600); err != nil {
		return "", err
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// Remove deletes clusterName's copy, if there is one.
func Remove(clusterName string) error {
	dir, err := Dir(clusterName)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("could not remove the lab's resources: %w", err)
	}
	return nil
}

// copyBudget enforces the limits across one Snapshot.
type copyBudget struct {
	files int
	bytes int64
}

// copyTree copies a file or folder; links are refused, files keep only
// their owner's executable bit (0700/0600).
func (b *copyBudget) copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a link — resources must be real files", rel)
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s isn't a regular file", rel)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		b.files++
		b.bytes += fi.Size()
		switch {
		case b.files > maxFiles:
			return fmt.Errorf("more than %d files", maxFiles)
		case fi.Size() > maxFileBytes:
			return fmt.Errorf("%s is larger than %d MB", rel, maxFileBytes>>20)
		case b.bytes > maxTotal:
			return fmt.Errorf("resources add up to more than %d MB", maxTotal>>20)
		}
		mode := os.FileMode(0o600)
		if fi.Mode()&0o100 != 0 {
			mode = 0o700
		}
		return copyFile(p, target, mode)
	})
}

func copyFile(src, dst string, mode os.FileMode) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, io.LimitReader(in, maxFileBytes+1))
	return err
}

// Load reads clusterName's copy: its resources and the folder they're in.
// No copy (the lab has none, or never ran) is an empty list, not an error.
func Load(clusterName string) ([]Resource, string, error) {
	dir, err := Dir(clusterName)
	if err != nil {
		return nil, "", err
	}
	b, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, dir, nil
	}
	if err != nil {
		return nil, "", err
	}
	var list []Resource
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, "", fmt.Errorf("the copy of %s's resources is damaged (%s) — `astrona reset` copies them again: %w", clusterName, manifestFile, err)
	}
	for _, r := range list {
		if _, err := r.Path(dir); err != nil {
			return nil, "", err
		}
	}
	return list, dir, nil
}

// Find looks a resource up by name (case-insensitive).
func Find(list []Resource, name string) (Resource, bool) {
	for _, r := range list {
		if strings.EqualFold(r.Name, name) {
			return r, true
		}
	}
	return Resource{}, false
}

// Path is where r is inside dir, its lab's copy — never outside it.
func (r Resource) Path(dir string) (string, error) {
	p, err := config.JoinStrictlyWithinBaseDir(dir, filepath.FromSlash(r.File))
	if err != nil {
		return "", fmt.Errorf("resource %s: %w", r.Name, err)
	}
	return p, nil
}

// Labs are the labs that have a copy of their resources (cluster names).
func Labs() ([]string, error) {
	base, err := Dir("astrona-lab")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Dir(base))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(base), e.Name(), manifestFile)); err == nil {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// CopyTo copies r out of its lab's copy (dir) to dest — a folder to copy it
// into, or the path it should get — and returns where it went. An existing
// file is only replaced with force; files are written 0644/0755 (they're
// the student's now), links are never followed.
func (r Resource) CopyTo(dir, dest string, force bool) (string, error) {
	src, err := r.Path(dir)
	if err != nil {
		return "", err
	}
	if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
		dest = filepath.Join(dest, path.Base(r.File))
	}
	if _, err := os.Lstat(dest); err == nil {
		if !force {
			return "", fmt.Errorf("%s already exists — pass --force to replace it", dest)
		}
		if err := os.RemoveAll(dest); err != nil {
			return "", err
		}
	}
	var budget copyBudget
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dest, rel)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a link", rel)
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case !d.Type().IsRegular():
			return fmt.Errorf("%s isn't a regular file", rel)
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		if budget.files++; budget.files > maxFiles {
			return fmt.Errorf("more than %d files", maxFiles)
		}
		mode := os.FileMode(0o644)
		if fi.Mode()&0o100 != 0 {
			mode = 0o755
		}
		return copyFile(p, target, mode)
	})
	if err != nil {
		return "", fmt.Errorf("copy %s: %w", r.Name, err)
	}
	return dest, nil
}
