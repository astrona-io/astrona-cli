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
	rt, err := os.OpenRoot(root) // every read below stays inside resources/
	if err != nil {
		return nil, fmt.Errorf("open %s/: %w", config.ResourcesDir, err)
	}
	defer rt.Close()

	qemu := cfg.Runtime.Type == "qemu"
	var out []Resource
	covered := map[string]bool{} // top-level names an entry covers
	for i, e := range cfg.Resources {
		file := path.Clean(strings.TrimSpace(e.File))
		if _, err := config.JoinStrictlyWithinBaseDir(root, filepath.FromSlash(file)); err != nil {
			return nil, fmt.Errorf("resources[%d]: %w", i, err)
		}
		fi, err := lstatNoLinks(rt, file)
		switch {
		case errors.Is(err, errLink):
			return nil, fmt.Errorf("resources[%d]: %w", i, err)
		case err != nil:
			return nil, fmt.Errorf("resources[%d]: %s/%s doesn't exist", i, config.ResourcesDir, file)
		}
		r := Resource{File: file, Description: strings.TrimSpace(e.Description), VM: e.VM, Dir: fi.IsDir()}
		r.Name = nameOf(file)
		switch {
		case e.Type == config.ResourceTypeFile:
			r.How = HowFile
		case strings.TrimSpace(e.Run) != "":
			r.How, r.Run = HowCommand, strings.TrimSpace(e.Run)
		default:
			r.How = howOf(rt, file, fi, qemu)
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
		fi, err := lstatNoLinks(rt, de.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, Resource{
			Name: nameOf(de.Name()), File: de.Name(), Dir: fi.IsDir(),
			How: howOf(rt, de.Name(), fi, qemu),
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

// errLink marks a resource path with a link in it.
var errLink = errors.New("is a link — resources must be real files")

// lstatNoLinks is Lstat of file (slash-separated, inside rt) that refuses
// a link anywhere in its path — a linked folder in the middle
// (home/.ssh/id_ed25519 with home → ~) as much as the file itself. rt
// already stops anything resolving outside it; this also keeps links
// inside it out, so what's copied is exactly what's in the lab.
func lstatNoLinks(rt *os.Root, file string) (fs.FileInfo, error) {
	var fi fs.FileInfo
	parts := strings.Split(file, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		var err error
		if fi, err = rt.Lstat(filepath.FromSlash(p)); err != nil {
			return nil, err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s/%s %w", config.ResourcesDir, p, errLink)
		}
	}
	return fi, nil
}

// howOf picks how a resource (file, inside rt) without a run command or
// type is used.
func howOf(rt *os.Root, file string, fi fs.FileInfo, qemu bool) string {
	if fi.IsDir() {
		return HowFile
	}
	switch strings.ToLower(path.Ext(file)) {
	case ".sh":
		return HowBash
	case ".yaml", ".yml":
		if qemu {
			return HowFile // no cluster to apply it to
		}
		return HowApply
	}
	if fi.Mode()&0o111 != 0 && hasShebang(rt, file) {
		return HowExec
	}
	return HowFile
}

func hasShebang(rt *os.Root, file string) bool {
	f, err := rt.Open(filepath.FromSlash(file))
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
	rt, err := openNoLink(filepath.Join(baseDir, config.ResourcesDir))
	if err != nil {
		return "", err
	}
	defer rt.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dest), "."+clusterName+"-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp) // gone after the rename; cleans up on error

	budget := copyBudget{dirMode: 0o700, fileMode: 0o600, execMode: 0o700}
	for _, r := range list {
		if !fs.ValidPath(r.File) || r.File == "." {
			return "", fmt.Errorf("%s/%s: not a path inside %s/", config.ResourcesDir, r.File, config.ResourcesDir)
		}
		dst := filepath.Join(tmp, filepath.FromSlash(r.File))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", err
		}
		if err := budget.copyTree(rt, r.File, dst); err != nil {
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

// openNoLink opens dir as an os.Root, refusing dir itself being a link
// (os.OpenRoot would follow it).
func openNoLink(dir string) (*os.Root, error) {
	fi, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return nil, fmt.Errorf("%s must be a folder (not a link)", dir)
	}
	return os.OpenRoot(dir)
}

// copyBudget enforces the limits across one copy, and the modes it
// writes: folders get dirMode, files fileMode — execMode when their
// owner may run them.
type copyBudget struct {
	files int
	bytes int64

	dirMode, fileMode, execMode os.FileMode
}

// copyTree copies src (a file or folder, slash-separated, inside rt) to
// dst. Every read goes through rt, so nothing outside it is reached, and a
// link anywhere — in src's path or under it — is refused.
func (b *copyBudget) copyTree(rt *os.Root, src, dst string) error {
	if _, err := lstatNoLinks(rt, src); err != nil {
		return err
	}
	return fs.WalkDir(rt.FS(), src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := "."
		if p != src {
			rel = strings.TrimPrefix(p, src+"/")
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s %w", rel, errLink)
		}
		if d.IsDir() {
			return os.MkdirAll(target, b.dirMode)
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
		mode := b.fileMode
		if fi.Mode()&0o100 != 0 {
			mode = b.execMode
		}
		return copyFile(rt, p, target, mode)
	})
}

// copyFile copies src (slash-separated, inside rt) to a new file dst.
func copyFile(rt *os.Root, src, dst string, mode os.FileMode) (err error) {
	in, err := rt.Open(filepath.FromSlash(src))
	if err != nil {
		return err
	}
	defer in.Close()
	if fi, err := in.Stat(); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%s isn't a regular file", src)
	}
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
	if _, err := r.Path(dir); err != nil {
		return "", err
	}
	rt, err := openNoLink(dir)
	if err != nil {
		return "", err
	}
	defer rt.Close()
	if _, err := lstatNoLinks(rt, r.File); err != nil { // before replacing anything
		return "", fmt.Errorf("copy %s: %w", r.Name, err)
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
	budget := copyBudget{dirMode: 0o755, fileMode: 0o644, execMode: 0o755}
	if err := budget.copyTree(rt, r.File, dest); err != nil {
		return "", fmt.Errorf("copy %s: %w", r.Name, err)
	}
	return dest, nil
}
