package authoring

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"astrona/internal/addons"
	"astrona/internal/apphome"
	"astrona/internal/bundle"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/lifecycle"
	"astrona/internal/ui"
)

// BundleRefusal explains why a lab can't be bundled, or "" if it can.
func BundleRefusal(cfg *config.LabConfig, nodeImage string) string {
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return "only kind labs can be bundled (qemu labs cache their own base images)"
	}
	for _, ref := range labops.ResourceRefs(cfg) {
		if strings.EqualFold(ref.Item.Type, "url") {
			return fmt.Sprintf("%s is fetched from a URL at run time (%s) — make it a local file to bundle the lab", ref.Where, ref.Item.Source)
		}
	}
	if k := cfg.Runtime.Kind; k != nil && k.Addons.GatewayAPI != "" {
		return addons.ErrNotBundleable.Error()
	}
	for _, l := range cfg.KindClusters() {
		if l.Addons.GatewayAPI != "" {
			return fmt.Sprintf("linked cluster '%s': %s", l.Name, addons.ErrNotBundleable)
		}
		// Offline, an unpinned cluster would boot kind's default image —
		// which may not be the one in the bundle.
		if l.Cluster().NodeImage() == "" {
			return fmt.Sprintf("linked cluster '%s' doesn't pin its node image — set its version (or image)", l.Name)
		}
	}
	if nodeImage == "" {
		return "the node image isn't pinned — set runtime.kind.version (or image) in the lab, or pass --node-image (kind's built-in default can't be determined reliably)"
	}
	return ""
}

// BundleOptions are the inputs of `astrona bundle create` beyond the lab.
type BundleOptions struct {
	Output    string // bundle file to write (.tar.gz)
	NodeImage string // "" = the lab's pinned runtime.kind node image
	CreatedBy string // astrona version recorded in the manifest
}

// PrepareBundle checks, before anything is written, that a loaded lab can
// be bundled, and returns the node image to bundle (opts.NodeImage, or the
// lab's pinned one).
func PrepareBundle(cfg *config.LabConfig, baseDir string, opts BundleOptions) (string, error) {
	if strings.HasPrefix(baseDir, "http") {
		return "", fmt.Errorf("bundle a local or --git lab (a URL-only config has no lab directory to pack)")
	}
	if err := lifecycle.Validate(cfg); err != nil {
		return "", err
	}
	nodeImage := opts.NodeImage
	if nodeImage == "" {
		nodeImage = cfg.Runtime.Kind.NodeImage()
	}
	if reason := BundleRefusal(cfg, nodeImage); reason != "" {
		return "", fmt.Errorf("can't bundle %s: %s", cfg.Metadata.Name, reason)
	}
	return nodeImage, nil
}

// BundleResult is a written bundle: its file, manifest and size.
type BundleResult struct {
	Path     string
	Manifest bundle.Manifest
	Size     int64 // bytes; 0 if the written file can't be stat'ed
}

// CreateBundle packs a lab (already checked by PrepareBundle, which gave
// nodeImage) with every image and addon manifest it would download into
// opts.Output. Progress goes to rep's steps.
func CreateBundle(rep *ui.Reporter, cfg *config.LabConfig, baseDir, nodeImage string, opts BundleOptions) (*BundleResult, error) {
	out := opts.Output
	work, err := os.MkdirTemp("", "astrona-bundle-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	m := bundle.Manifest{
		FormatVersion: bundle.FormatVersion, Lab: cfg.Metadata.Name, Arch: goruntime.GOARCH,
		CreatedBy: opts.CreatedBy, CreatedAt: time.Now().UTC(), NodeImage: nodeImage,
	}
	var entries []bundle.Entry

	// The lab's own cluster and every linked cluster: node images,
	// preload images, addon manifests — each once.
	type want struct{ ref, purpose string }
	wanted := []want{}
	seen := map[string]bool{}
	add := func(ref, purpose string) {
		if !seen[ref] {
			seen[ref] = true
			wanted = append(wanted, want{ref, purpose})
		}
	}
	add(nodeImage, "node")
	for _, im := range labops.LabPreloadImages(cfg, baseDir) {
		add(im, "preload")
	}
	addonSets := []config.KindAddons{}
	if k := cfg.Runtime.Kind; k != nil && !k.Addons.IsZero() {
		addonSets = append(addonSets, k.Addons)
	}
	for _, l := range cfg.KindClusters() {
		add(l.Cluster().NodeImage(), "node")
		for _, im := range l.PreloadImages {
			add(im, "preload")
		}
		if !l.Addons.IsZero() {
			addonSets = append(addonSets, l.Addons)
		}
	}

	var addonManifests []addons.BundledManifest
	if len(addonSets) > 0 {
		t := rep.Step("Collect addon manifests")
		have := map[string]bool{}
		for _, set := range addonSets {
			ms, err := addons.ForBundle(set, t.Output())
			if err != nil {
				return nil, t.Fail(err)
			}
			for _, am := range ms {
				if !have[am.SHA256] {
					have[am.SHA256] = true
					addonManifests = append(addonManifests, am)
				}
			}
		}
		t.Done()
	}

	for _, am := range addonManifests {
		name := path.Join("addons", am.SHA256+".yaml")
		m.AddonManifests = append(m.AddonManifests, bundle.AddonManifest{Addon: am.Addon, File: name, SHA256: am.SHA256})
		entries = append(entries, bundle.Entry{Name: name, Path: am.Path})
		for _, im := range am.Images {
			add(im, "addon")
		}
	}

	for i, w := range wanted {
		t := rep.Step("Save image %s", w.ref)
		local := filepath.Join(work, fmt.Sprintf("%d.tar", i))
		if err := cluster.SaveImage(w.ref, local, t.Output()); err != nil {
			return nil, t.Fail(err)
		}
		sum, err := bundle.SHA256File(local)
		if err != nil {
			return nil, t.Fail(err)
		}
		name := fmt.Sprintf("images/%d.tar", i)
		m.Images = append(m.Images, bundle.Image{Ref: w.ref, File: name, SHA256: sum, Purpose: w.purpose})
		entries = append(entries, bundle.Entry{Name: name, Path: local})
		t.Done()
	}

	lab, err := bundle.LabEntries(baseDir)
	if err != nil {
		return nil, err
	}
	entries = append(lab, entries...)

	t := rep.Step("Write %s", out)
	if err := bundle.Write(out, m, entries); err != nil {
		return nil, t.Fail(err)
	}
	t.Done()

	res := &BundleResult{Path: out, Manifest: m}
	if info, err := os.Stat(out); err == nil {
		res.Size = info.Size()
	}
	return res, nil
}

// BundlesDir is ~/.astrona/bundles, where `astrona bundle load` unpacks labs.
func BundlesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, apphome.Name(), "bundles"), nil
}

// OpenedBundle is a bundle verified and unpacked into a staging directory
// under BundlesDir, not yet installed. Close removes the staging directory.
type OpenedBundle struct {
	SHA256   string // of the bundle file; the trust pin
	Manifest *bundle.Manifest
	Config   *config.LabConfig // the bundle's lab, for the trust summary
	root     string
	staging  string
}

// OpenBundle hashes and extracts a bundle (verifying every checksum), checks
// it was built for this CPU architecture and parses its lab config. Nothing
// is installed: the caller asks for trust, then calls Install.
func OpenBundle(src string) (*OpenedBundle, error) {
	sum, err := bundle.SHA256File(src)
	if err != nil {
		return nil, err
	}
	root, err := BundlesDir()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	staging, err := os.MkdirTemp(root, ".load-*")
	if err != nil {
		return nil, err
	}
	ob := &OpenedBundle{SHA256: sum, root: root, staging: staging}

	m, err := bundle.Extract(src, staging)
	if err != nil {
		ob.Close()
		return nil, fmt.Errorf("invalid bundle: %w", err)
	}
	if m.Arch != goruntime.GOARCH {
		ob.Close()
		return nil, fmt.Errorf("bundle was built for %s, this machine is %s — its images won't run here", m.Arch, goruntime.GOARCH)
	}
	cfg, cleanup, err := config.LoadLabConfig(filepath.Join(ob.labDir(), "config.yaml"))
	if err != nil {
		ob.Close()
		return nil, fmt.Errorf("bundle's lab: %w", err)
	}
	cleanup()
	ob.Manifest, ob.Config = m, cfg
	return ob, nil
}

func (ob *OpenedBundle) labDir() string { return filepath.Join(ob.staging, "lab") }

// Close removes the staging directory (safe after Install).
func (ob *OpenedBundle) Close() { os.RemoveAll(ob.staging) }

// Install loads the bundle's images into the container engine, seeds the
// addon cache and moves the lab into BundlesDir. Returns the lab's
// directory. Progress goes to rep's steps.
func (ob *OpenedBundle) Install(rep *ui.Reporter) (string, error) {
	m, labDir := ob.Manifest, ob.labDir()
	for _, im := range m.Images {
		t := rep.Step("Load image %s", im.Ref)
		if err := cluster.LoadImageArchive(filepath.Join(ob.staging, filepath.FromSlash(im.File)), t.Output()); err != nil {
			return "", t.Fail(err)
		}
		t.Done()
	}
	for _, am := range m.AddonManifests {
		t := rep.Step("Cache %s manifest", am.Addon)
		data, err := os.ReadFile(filepath.Join(ob.staging, filepath.FromSlash(am.File)))
		if err == nil {
			err = addons.SeedCache(data)
		}
		if err != nil {
			return "", t.Fail(err)
		}
		t.Done()
	}
	if extra := m.AddonImages(); len(extra) > 0 {
		data, _ := json.Marshal(extra)
		if err := os.WriteFile(filepath.Join(labDir, labops.BundleImagesSidecar), data, 0600); err != nil {
			return "", err
		}
	}

	// bundle.Extract already validated m.Lab; dest is RemoveAll'd,
	// so check again that it's a direct child of root.
	dest := filepath.Join(ob.root, fmt.Sprintf("%s-%s", m.Lab, ob.SHA256[:12]))
	if filepath.Dir(dest) != filepath.Clean(ob.root) {
		return "", fmt.Errorf("bundle lab name '%s' escapes %s", m.Lab, ob.root)
	}
	if err := os.RemoveAll(dest); err != nil {
		return "", err
	}
	if err := os.Rename(labDir, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// InspectBundle extracts a bundle into a temporary directory (verifying
// every checksum), removes it again and returns the manifest.
func InspectBundle(src string) (*bundle.Manifest, error) {
	tmp, err := os.MkdirTemp("", "astrona-inspect-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	m, err := bundle.Extract(src, tmp)
	if err != nil {
		return nil, fmt.Errorf("invalid bundle: %w", err)
	}
	return m, nil
}
