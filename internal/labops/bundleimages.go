package labops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"astrona/internal/config"
)

// BundleImagesSidecar, written into a loaded bundle's lab directory, lists
// the addon images that have to be loaded into the kind nodes (the lab's
// own preloadImages are in its config already).
const BundleImagesSidecar = ".astrona-bundle-images.json"

// ApplyBundleImages adds a loaded bundle's addon images to the lab's
// preloadImages — and to those of each linked cluster that installs
// addons — so `run`/`test`/`reset` load them into the nodes and addons
// install without a registry. No-op for every other lab.
func ApplyBundleImages(cfg *config.LabConfig, baseDir string) {
	extra := bundleAddonImages(baseDir)
	images := mergeImages(labOwnPreloadImages(cfg), extra)
	if cfg.Runtime.Kind == nil {
		if len(images) == 0 {
			return
		}
		cfg.Runtime.Kind = &config.KindConfig{}
	}
	cfg.Runtime.Kind.PreloadImages = images
	for i, l := range cfg.Runtime.Kind.Clusters {
		if !l.Addons.IsZero() {
			cfg.Runtime.Kind.Clusters[i].PreloadImages = mergeImages(l.PreloadImages, extra)
		}
	}
}

// LabPreloadImages is what to load into the lab's own nodes: its
// preloadImages plus, for a lab loaded from a bundle, the addon images.
func LabPreloadImages(cfg *config.LabConfig, baseDir string) []string {
	return mergeImages(labOwnPreloadImages(cfg), bundleAddonImages(baseDir))
}

func labOwnPreloadImages(cfg *config.LabConfig) []string {
	if cfg.Runtime.Kind == nil {
		return nil
	}
	return cfg.Runtime.Kind.PreloadImages
}

// bundleAddonImages reads a loaded bundle's addon image list (none for any
// other lab).
func bundleAddonImages(baseDir string) []string {
	data, err := os.ReadFile(filepath.Join(baseDir, BundleImagesSidecar))
	if err != nil {
		return nil
	}
	var extra []string
	if json.Unmarshal(data, &extra) != nil {
		return nil
	}
	return extra
}

// mergeImages is images followed by every extra one not already in it.
func mergeImages(images, extra []string) []string {
	out := append([]string(nil), images...)
	for _, im := range extra {
		if !slices.Contains(out, im) {
			out = append(out, im)
		}
	}
	return out
}
