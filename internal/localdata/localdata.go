// Package localdata inventories and removes explicitly disposable CLI files.
package localdata

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"astrona/internal/apphome"
)

type Store struct {
	Data  string
	Repos string
}

func DefaultStore() (Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Store{}, err
	}
	repos, err := apphome.ReposDir()
	if err != nil {
		return Store{}, err
	}
	return Store{Data: filepath.Join(home, apphome.Name()), Repos: repos}, nil
}

type Usage struct {
	Path     string `json:"path"`
	Files    int64  `json:"files"`
	Bytes    int64  `json:"bytes"`
	Symlinks int64  `json:"symlinks"`
	modified time.Time
}

// Usage reports logical file sizes, not allocated disk blocks. Symlink targets
// and container-engine storage are never inspected.
func (s Store) Usage(ctx context.Context) ([]Usage, error) {
	result := []Usage{}
	bases := []string{s.Data}
	// Development Git caches are already counted beneath Data/cache.
	if rel, err := filepath.Rel(s.Data, s.Repos); err != nil || !filepath.IsLocal(rel) {
		bases = append(bases, s.Repos)
	}
	for _, base := range bases {
		var r *os.Root
		var err error
		if base == s.Repos {
			r, err = s.openRepos()
		} else {
			r, err = openDir(base)
		}
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		entries, err := fs.ReadDir(r.FS(), ".")
		if err != nil {
			r.Close()
			return nil, err
		}
		for _, entry := range entries {
			u, err := measure(ctx, r, entry.Name(), filepath.Join(base, entry.Name()))
			if err != nil {
				r.Close()
				return nil, err
			}
			result = append(result, u)
		}
		r.Close()
	}
	return result, nil
}

func measure(ctx context.Context, r *os.Root, name, path string) (Usage, error) {
	u := Usage{Path: path}
	if err := ctx.Err(); err != nil {
		return u, err
	}
	info, err := r.Lstat(name)
	if err != nil {
		return u, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		u.Symlinks = 1
		return u, nil
	}
	err = fs.WalkDir(r.FS(), name, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			u.Symlinks++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(u.modified) {
			u.modified = info.ModTime()
		}
		if info.Mode().IsRegular() {
			if info.Size() > math.MaxInt64-u.Bytes {
				return fmt.Errorf("cache size exceeds supported range at %s", path)
			}
			u.Files++
			u.Bytes += info.Size()
		}
		return nil
	})
	return u, err
}

type Item struct {
	Usage
	base     string
	name     string
	info     os.FileInfo
	baseInfo os.FileInfo
}

type Plan struct {
	Items   []Item   `json:"items"`
	Skipped []string `json:"skipped"`
	Removed int      `json:"removed"`
	Applied bool     `json:"applied"`
}

// PlanClean keeps every cache when a saved lab may still depend on it. This
// includes stopped VMs whose disks can reference cached backing images.
func (s Store) PlanClean(ctx context.Context, tempOnly bool, now time.Time) (Plan, error) {
	p := Plan{Items: []Item{}, Skipped: []string{}}
	r, err := openDir(s.Data)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return p, err
	}
	if r != nil {
		defer r.Close()
		for _, name := range []string{"labs", "kind", "qemu", "portforward", "exams"} {
			info, err := r.Lstat(name)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return p, err
			}
			if !info.IsDir() {
				return p, fmt.Errorf("cannot verify saved lab state at %s", filepath.Join(s.Data, name))
			}
			entries, err := fs.ReadDir(r.FS(), name)
			if err != nil {
				return p, err
			}
			if len(entries) > 0 {
				p.Skipped = append(p.Skipped, "Caches kept: saved lab state exists in "+filepath.Join(s.Data, name)+". Use astrona list and destroy labs you no longer need; stopped labs still need their files.")
				return p, nil
			}
		}
	}
	for _, rel := range []string{"cache/addons", "cache/images"} {
		base := filepath.Join(s.Data, filepath.FromSlash(rel))
		if r == nil {
			continue
		}
		cacheRoot, err := openChild(r, "cache")
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return p, err
		}
		child, err := openChild(cacheRoot, filepath.Base(rel))
		cacheRoot.Close()
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return p, err
		}
		err = p.addChildren(ctx, child, base, tempOnly, now)
		child.Close()
		if err != nil {
			return p, err
		}
	}
	if !tempOnly {
		child, err := s.openRepos()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return p, err
		}
		if child != nil {
			err = p.addChildren(ctx, child, s.Repos, false, now)
			child.Close()
			if err != nil {
				return p, err
			}
		}
		if r != nil {
			for _, name := range []string{"catalog.json", "update-check.json"} {
				if err := p.addFile(ctx, r, s.Data, name); err != nil {
					return p, err
				}
			}
		}
	}
	return p, nil
}

func (p *Plan) addChildren(ctx context.Context, r *os.Root, base string, tempOnly bool, now time.Time) error {
	baseInfo, err := r.Stat(".")
	if err != nil {
		return err
	}
	entries, err := fs.ReadDir(r.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		name := entry.Name()
		if tempOnly {
			partial := (strings.HasPrefix(name, ".cache-") && strings.HasSuffix(name, ".tmp")) || strings.HasPrefix(name, ".dl-")
			if !partial || !info.Mode().IsRegular() || now.Sub(info.ModTime()) < 24*time.Hour {
				continue
			}
		}
		// Links are not cache contents. Leave them for the user to inspect.
		if info.Mode()&os.ModeSymlink != 0 {
			p.Skipped = append(p.Skipped, "Symlink kept: "+filepath.Join(base, name))
			continue
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			p.Skipped = append(p.Skipped, "Special file kept: "+filepath.Join(base, name))
			continue
		}
		u, err := measure(ctx, r, name, filepath.Join(base, name))
		if err != nil {
			return err
		}
		p.Items = append(p.Items, Item{Usage: u, base: base, name: name, info: info, baseInfo: baseInfo})
	}
	return nil
}

func (p *Plan) addFile(ctx context.Context, r *os.Root, base, name string) error {
	info, err := r.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing non-regular file %s", filepath.Join(base, name))
	}
	u, err := measure(ctx, r, name, filepath.Join(base, name))
	if err != nil {
		return err
	}
	baseInfo, err := r.Stat(".")
	if err != nil {
		return err
	}
	p.Items = append(p.Items, Item{Usage: u, base: base, name: name, info: info, baseInfo: baseInfo})
	return nil
}

// PlanReset deliberately excludes credentials, trust approvals, catalog sources,
// results and lab state. It also works when the settings JSON is malformed.
func (s Store) PlanReset(ctx context.Context) (Plan, error) {
	p := Plan{Items: []Item{}, Skipped: []string{}}
	r, err := openDir(s.Data)
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	defer r.Close()
	for _, name := range []string{"settings.json", "settings-review.json", "current.json", "update-check.json", "catalog.json"} {
		if err := p.addFile(ctx, r, s.Data, name); err != nil {
			return p, err
		}
	}
	return p, nil
}

// Apply checks the whole plan before deleting anything, then stops at the first
// error. Callers hold the exclusive maintenance lock from planning to completion.
func (p *Plan) Apply(ctx context.Context) error {
	roots := map[string]*os.Root{}
	defer func() {
		for _, r := range roots {
			r.Close()
		}
	}()
	for _, item := range p.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		r := roots[item.base]
		if r == nil {
			var err error
			r, err = openDir(item.base)
			if err != nil {
				return err
			}
			roots[item.base] = r
		}
		baseInfo, err := r.Stat(".")
		if err != nil {
			return err
		}
		if !os.SameFile(baseInfo, item.baseInfo) {
			return fmt.Errorf("cache directory %s changed since the preview; try again", item.base)
		}
		info, err := r.Lstat(item.name)
		if err != nil {
			return err
		}
		if !os.SameFile(info, item.info) || !info.ModTime().Equal(item.info.ModTime()) || info.Size() != item.info.Size() {
			return fmt.Errorf("%s changed since the preview; try again", item.Path)
		}
	}
	for _, item := range p.Items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := roots[item.base].RemoveAll(item.name); err != nil {
			return fmt.Errorf("remove %s (%d entries already removed): %w", item.Path, p.Removed, err)
		}
		p.Removed++
	}
	p.Applied = true
	return nil
}

// openDir rejects symlinks in the managed directory itself. Root operations keep
// traversal inside that directory even when a nested symlink changes concurrently.
func openDir(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == string(filepath.Separator) {
		return nil, fmt.Errorf("unsafe local data path %q", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("local data path %s must be a directory, not a symlink", path)
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		r.Close()
		return nil, fmt.Errorf("local data directory %s changed while opening it", path)
	}
	return r, nil
}

func openChild(r *os.Root, name string) (*os.Root, error) {
	info, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("refusing non-directory cache path %s", name)
	}
	child, err := r.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		child.Close()
		return nil, fmt.Errorf("cache path %s changed while opening it", name)
	}
	return child, nil
}

func (s Store) openRepos() (*os.Root, error) {
	parent, err := openDir(filepath.Dir(s.Repos))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return openChild(parent, filepath.Base(s.Repos))
}

// Lock coordinates current CLI processes. The lock inode must never be removed.
// Shared locks let ordinary commands overlap; maintenance takes an exclusive lock.
func (s Store) Lock(exclusive bool) (func(), error) {
	if !filepath.IsAbs(s.Data) || filepath.Clean(s.Data) == string(filepath.Separator) {
		return nil, fmt.Errorf("unsafe local data path %q", s.Data)
	}
	if err := os.MkdirAll(s.Data, 0o700); err != nil {
		return nil, err
	}
	r, err := openDir(s.Data)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return lockFile(r, ".maintenance.lock", exclusive)
}

// LockSettings also coordinates with the settings writer shipped separately.
func (s Store) LockSettings() (func(), error) {
	r, err := openDir(s.Data)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return lockFile(r, "settings.json.lock", true)
}

func lockFile(r *os.Root, name string, exclusive bool) (func(), error) {
	if info, err := r.Lstat(name); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s must be a regular lock file", name)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := r.OpenFile(name, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s must be a regular lock file", name)
	}
	// os.Root resolves in-root symlinks itself, so O_NOFOLLOW alone does not
	// reject them. Check the directory entry against the opened descriptor too.
	entry, err := r.Lstat(name)
	if err != nil || !entry.Mode().IsRegular() || !os.SameFile(entry, info) {
		f.Close()
		return nil, fmt.Errorf("%s changed while opening the lock", name)
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another Astrona command is using local data; wait for it to finish and retry: %w", err)
	}
	return func() { f.Close() }, nil
}
