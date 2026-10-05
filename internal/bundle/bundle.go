// Package bundle packs a kind lab and everything it downloads at run time
// — node image, preload images, addon manifests and their images — into
// one .tar.gz, and unpacks it on a machine without internet access.
//
// Layout:
//
//	astrona-bundle.json   Manifest (first entry)
//	lab/...               the lab directory (config, docs, scripts, manifests)
//	images/<n>.tar        one `docker/podman save` archive per image
//	addons/<sha256>.yaml  addon manifests, as pinned by astrona's catalog
//
// Every image archive and addon manifest is recorded with its SHA-256 and
// verified on load; extraction refuses absolute paths, "..", links and
// anything over the size limits.
package bundle

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	ManifestName  = "astrona-bundle.json"
	FormatVersion = 1
	// Limits for extraction — a bundle can legitimately hold several GB
	// of images, but not unbounded data or file counts.
	maxTotalBytes = 30 << 30
	maxEntries    = 100_000
)

// Image is one container image in the bundle.
type Image struct {
	Ref     string `json:"ref"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Purpose string `json:"purpose"` // node | preload | addon
}

// AddonManifest is one addon manifest in the bundle.
type AddonManifest struct {
	Addon  string `json:"addon"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

// Manifest describes a bundle.
type Manifest struct {
	FormatVersion  int             `json:"formatVersion"`
	Lab            string          `json:"lab"`
	Arch           string          `json:"arch"`
	CreatedBy      string          `json:"createdBy"`
	CreatedAt      time.Time       `json:"createdAt"`
	NodeImage      string          `json:"nodeImage"`
	Images         []Image         `json:"images"`
	AddonManifests []AddonManifest `json:"addonManifests"`
}

// AddonImages are the images addons run inside the cluster — they must be
// loaded into the nodes, not just the host engine.
func (m Manifest) AddonImages() []string {
	var out []string
	for _, im := range m.Images {
		if im.Purpose == "addon" {
			out = append(out, im.Ref)
		}
	}
	return out
}

// Entry is a file to put in the bundle: Name inside the archive, Path on
// disk.
type Entry struct {
	Name, Path string
}

// Write creates the bundle at out: the manifest first, then entries.
func Write(out string, m Manifest, entries []Entry) error {
	f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("create bundle: %w", err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	err = func() error {
		mdata, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: ManifestName, Mode: 0644, Size: int64(len(mdata)), ModTime: m.CreatedAt}); err != nil {
			return err
		}
		if _, err := tw.Write(mdata); err != nil {
			return err
		}
		for _, e := range entries {
			if err := addFile(tw, e); err != nil {
				return fmt.Errorf("add %s: %w", e.Name, err)
			}
		}
		if err := tw.Close(); err != nil {
			return err
		}
		return gz.Close()
	}()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out)
	}
	return err
}

func addFile(tw *tar.Writer, e Entry) error {
	src, err := os.Open(e.Path)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	mode := int64(0644)
	if info.Mode()&0111 != 0 {
		mode = 0755
	}
	if err := tw.WriteHeader(&tar.Header{Name: e.Name, Mode: mode, Size: info.Size(), ModTime: info.ModTime()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, src)
	return err
}

// LabEntries lists every regular file under labDir as "lab/<rel>",
// skipping .git and refusing symlinks (a bundle must not depend on, or
// leak, anything outside the lab).
func LabEntries(labDir string) ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(labDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink — bundles only contain regular files", p)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(labDir, p)
		if err != nil {
			return err
		}
		out = append(out, Entry{Name: path.Join("lab", filepath.ToSlash(rel)), Path: p})
		return nil
	})
	return out, err
}

// SHA256File hashes a file.
func SHA256File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// safeName validates an archive entry name: relative, clean, no "..".
func safeName(name string) (string, error) {
	clean := path.Clean(name)
	if name == "" || path.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(name, "\\") {
		return "", fmt.Errorf("unsafe path %q in bundle", name)
	}
	return clean, nil
}

// Extract unpacks the bundle at src into dest (which must not exist) and
// returns its manifest, after verifying every recorded hash. Only regular
// files and directories are accepted.
func Extract(src, dest string) (*Manifest, error) {
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("not a bundle (gzip): %w", err)
	}
	tr := tar.NewReader(gz)

	if err := os.MkdirAll(dest, 0700); err != nil {
		return nil, err
	}
	var total int64
	for n := 0; ; n++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read bundle: %w", err)
		}
		if n >= maxEntries {
			return nil, fmt.Errorf("bundle has more than %d entries", maxEntries)
		}
		name, err := safeName(hdr.Name)
		if err != nil {
			return nil, err
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0700); err != nil {
				return nil, err
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > maxTotalBytes {
				return nil, fmt.Errorf("bundle is larger than %d GB", maxTotalBytes>>30)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return nil, err
			}
			mode := os.FileMode(0644)
			if hdr.Mode&0111 != 0 {
				mode = 0755
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return nil, err
			}
			_, err = io.Copy(out, io.LimitReader(tr, hdr.Size))
			if cerr := out.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("bundle entry %q is not a regular file or directory", hdr.Name)
		}
	}

	data, err := os.ReadFile(filepath.Join(dest, ManifestName))
	if err != nil {
		return nil, fmt.Errorf("bundle has no %s", ManifestName)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ManifestName, err)
	}
	if m.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("bundle format %d isn't supported by this astrona (expects %d)", m.FormatVersion, FormatVersion)
	}
	for _, im := range m.Images {
		if err := verifyFile(dest, im.File, im.SHA256); err != nil {
			return nil, fmt.Errorf("image %s: %w", im.Ref, err)
		}
	}
	for _, am := range m.AddonManifests {
		if err := verifyFile(dest, am.File, am.SHA256); err != nil {
			return nil, fmt.Errorf("addon manifest %s: %w", am.Addon, err)
		}
	}
	return &m, nil
}

func verifyFile(dest, name, want string) error {
	clean, err := safeName(name)
	if err != nil {
		return err
	}
	got, err := SHA256File(filepath.Join(dest, filepath.FromSlash(clean)))
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("checksum mismatch (got %s, want %s)", got[:12], want[:min(12, len(want))])
	}
	return nil
}
