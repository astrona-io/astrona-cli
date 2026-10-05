package addons

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"astrona/internal/config"
)

// BundledManifest is an addon manifest prepared for an offline bundle: its
// verified local copy and the images it runs.
type BundledManifest struct {
	Addon  string
	SHA256 string
	Path   string
	Images []string
}

// imageLine matches `image: <ref>` in a Kubernetes manifest (quoted or
// not); good enough for the pinned upstream manifests in the catalog.
var imageLine = regexp.MustCompile(`(?m)^\s*-?\s*image:\s*["']?([^\s"']+)["']?\s*$`)

// ImagesIn lists the distinct container images a manifest references.
func ImagesIn(manifest []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range imageLine.FindAllSubmatch(manifest, -1) {
		ref := string(m[1])
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	sort.Strings(out)
	return out
}

// ErrNotBundleable is returned for addons whose runtime images can't all
// be determined from their manifest.
var ErrNotBundleable = fmt.Errorf("the gatewayAPI addon can't be bundled for offline use yet — Envoy Gateway starts its proxy image at runtime, which isn't listed in its manifest")

// ForBundle fetches (or reuses the verified cache of) every manifest the
// enabled addons need and returns them with their images.
func ForBundle(a config.KindAddons, log io.Writer) ([]BundledManifest, error) {
	if a.GatewayAPI != "" {
		return nil, ErrNotBundleable
	}
	var out []BundledManifest
	for _, ad := range selected(a) {
		path, err := fetch(ad.Manifest, log)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ad.Name, err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out = append(out, BundledManifest{Addon: ad.Name, SHA256: ad.Manifest.SHA256, Path: path, Images: ImagesIn(data)})
	}
	return out, nil
}

// SeedCache stores a manifest from a bundle in the addon cache — only if
// its content matches a manifest astrona's own catalog pins, so a bundle
// can never substitute what an addon installs.
func SeedCache(data []byte) error {
	for _, ad := range []addon{calico, certManager, metricsServer, envoyGateway} {
		if verify(data, ad.Manifest.SHA256) != nil {
			continue
		}
		dir, err := cacheDir()
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, ad.Manifest.SHA256+".yaml"), data, 0600)
	}
	return fmt.Errorf("manifest doesn't match any addon version this astrona pins — bundle made by a different astrona version?")
}
