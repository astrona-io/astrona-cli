package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"time"

	"astrona/internal/addons"
	"astrona/internal/bundle"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/lifecycle"
	"astrona/internal/trust"
	"astrona/internal/ui"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// bundleImagesSidecar, written into a loaded bundle's lab directory, lists
// the addon images that have to be loaded into the kind nodes (the lab's
// own preloadImages are in its config already).
const bundleImagesSidecar = ".astrona-bundle-images.json"

// applyBundleImages adds a loaded bundle's addon images to the lab's
// preloadImages — and to those of each linked cluster that installs
// addons — so `run`/`test`/`reset` load them into the nodes and addons
// install without a registry. No-op for every other lab.
func applyBundleImages(cfg *config.LabConfig, baseDir string) {
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

// labPreloadImages is what to load into the lab's own nodes: its
// preloadImages plus, for a lab loaded from a bundle, the addon images.
func labPreloadImages(cfg *config.LabConfig, baseDir string) []string {
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
	data, err := os.ReadFile(filepath.Join(baseDir, bundleImagesSidecar))
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

// bundleRefusal explains why a lab can't be bundled, or "" if it can.
func bundleRefusal(cfg *config.LabConfig, nodeImage string) string {
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != "kind" {
		return "only kind labs can be bundled (qemu labs cache their own base images)"
	}
	for _, ref := range resourceRefs(cfg) {
		if strings.EqualFold(ref.item.Type, "url") {
			return fmt.Sprintf("%s is fetched from a URL at run time (%s) — make it a local file to bundle the lab", ref.where, ref.item.Source)
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

func newBundleCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bundle",
		Short: "Pack a lab with everything it downloads, for machines without internet",
		Long: "Pack a kind lab with everything it would otherwise download — node image, preload " +
			"images, addon manifests and their images — into one .tar.gz, and load it on a machine " +
			"without internet access (classrooms, air-gapped environments).",
	}
	cmd.AddCommand(newBundleCreateCmd(flags), newBundleLoadCmd(flags), newBundleInspectCmd())
	return cmd
}

func newBundleCreateCmd(flags *rootFlags) *cobra.Command {
	var out, nodeImage string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an offline bundle of a lab",
		Example: `  astrona bundle create -c ./labs/k8s-web-01 -o k8s-web-01.tar.gz
  astrona bundle create -c . -o lab.tar.gz --node-image kindest/node:v1.31.2`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return fmt.Errorf("--output is required")
			}
			cfg, baseDir, cleanup, err := LoadLabForCommand(flags)
			if err != nil {
				return err
			}
			defer cleanup()
			if strings.HasPrefix(baseDir, "http") {
				return fmt.Errorf("bundle a local or --git lab (a URL-only config has no lab directory to pack)")
			}
			if err := lifecycle.Validate(cfg); err != nil {
				return err
			}
			if nodeImage == "" {
				nodeImage = cfg.Runtime.Kind.NodeImage()
			}
			if reason := bundleRefusal(cfg, nodeImage); reason != "" {
				return fmt.Errorf("can't bundle %s: %s", cfg.Metadata.Name, reason)
			}

			rep, err := ui.NewReporter("bundle", cfg.Metadata.Name, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			work, err := os.MkdirTemp("", "astrona-bundle-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(work)

			m := bundle.Manifest{
				FormatVersion: bundle.FormatVersion, Lab: cfg.Metadata.Name, Arch: goruntime.GOARCH,
				CreatedBy: Version, CreatedAt: time.Now().UTC(), NodeImage: nodeImage,
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
			for _, im := range labPreloadImages(cfg, baseDir) {
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
						return t.Fail(err)
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
					return t.Fail(err)
				}
				sum, err := bundle.SHA256File(local)
				if err != nil {
					return t.Fail(err)
				}
				name := fmt.Sprintf("images/%d.tar", i)
				m.Images = append(m.Images, bundle.Image{Ref: w.ref, File: name, SHA256: sum, Purpose: w.purpose})
				entries = append(entries, bundle.Entry{Name: name, Path: local})
				t.Done()
			}

			lab, err := bundle.LabEntries(baseDir)
			if err != nil {
				return err
			}
			entries = append(lab, entries...)

			t := rep.Step("Write %s", out)
			if err := bundle.Write(out, m, entries); err != nil {
				return t.Fail(err)
			}
			t.Done()
			rep.Close()

			info, _ := os.Stat(out)
			fmt.Printf("\nBundle %s — %s, %d image(s), %d addon manifest(s), %.0f MB, %s.\n", out, m.Lab, len(m.Images), len(m.AddonManifests), float64(info.Size())/(1<<20), m.Arch)
			fmt.Printf("On the offline machine: astrona bundle load %s\n", filepath.Base(out))
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "Bundle file to write (.tar.gz)")
	cmd.Flags().StringVar(&nodeImage, "node-image", "", "Node image to bundle when the lab doesn't pin runtime.kind.version/image")
	return cmd
}

// bundlesDir is ~/.astrona/bundles.
func bundlesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".astrona", "bundles"), nil
}

func newBundleLoadCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "load <bundle.tar.gz>",
		Short: "Load an offline bundle: verify it, load its images, unpack the lab",
		Long: "Verify a bundle (every image and addon manifest against its recorded SHA-256, the " +
			"CPU architecture), load its images into Docker/Podman, seed the addon cache (only " +
			"manifests matching astrona's own pinned versions are accepted), and unpack the lab " +
			"into ~/.astrona/bundles/. Then run it like any local lab, with no network.\n\n" +
			"A bundle is someone else's code: like a remote lab, you're shown what it will do and " +
			"asked once per bundle (--trust to approve without asking).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			sum, err := bundle.SHA256File(src)
			if err != nil {
				return err
			}
			root, err := bundlesDir()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(root, 0700); err != nil {
				return err
			}
			staging, err := os.MkdirTemp(root, ".load-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(staging)

			m, err := bundle.Extract(src, staging)
			if err != nil {
				return fmt.Errorf("invalid bundle: %w", err)
			}
			if m.Arch != goruntime.GOARCH {
				return fmt.Errorf("bundle was built for %s, this machine is %s — its images won't run here", m.Arch, goruntime.GOARCH)
			}
			labDir := filepath.Join(staging, "lab")
			cfg, cleanup, err := config.LoadLabConfig(filepath.Join(labDir, "config.yaml"))
			if err != nil {
				return fmt.Errorf("bundle's lab: %w", err)
			}
			cleanup()

			src = mustAbs(src)
			if err := trustBundle(flags, src, sum, cfg, m); err != nil {
				return err
			}

			rep, err := ui.NewReporter("bundle-load", m.Lab, flags.verbose)
			if err != nil {
				return err
			}
			defer rep.Close()

			for _, im := range m.Images {
				t := rep.Step("Load image %s", im.Ref)
				if err := cluster.LoadImageArchive(filepath.Join(staging, filepath.FromSlash(im.File)), t.Output()); err != nil {
					return t.Fail(err)
				}
				t.Done()
			}
			for _, am := range m.AddonManifests {
				t := rep.Step("Cache %s manifest", am.Addon)
				data, err := os.ReadFile(filepath.Join(staging, filepath.FromSlash(am.File)))
				if err == nil {
					err = addons.SeedCache(data)
				}
				if err != nil {
					return t.Fail(err)
				}
				t.Done()
			}
			if extra := m.AddonImages(); len(extra) > 0 {
				data, _ := json.Marshal(extra)
				if err := os.WriteFile(filepath.Join(labDir, bundleImagesSidecar), data, 0600); err != nil {
					return err
				}
			}

			dest := filepath.Join(root, fmt.Sprintf("%s-%s", m.Lab, sum[:12]))
			if err := os.RemoveAll(dest); err != nil {
				return err
			}
			if err := os.Rename(labDir, dest); err != nil {
				return err
			}
			rep.Close()
			fmt.Printf("\nLoaded %s into %s — works without internet now:\n  astrona run -c %s\n", m.Lab, dest, dest)
			return nil
		},
	}
}

// trustBundle asks before a bundle's images and lab are installed, pinned
// to the bundle file's SHA-256.
func trustBundle(flags *rootFlags, src, sum string, cfg *config.LabConfig, m *bundle.Manifest) error {
	tsrc := trust.Source{Kind: "bundle", Location: filepath.Base(src), Pin: "sha256:" + sum}
	status, prev, err := trust.Check(tsrc)
	if err != nil || status == trust.Trusted {
		return err
	}
	if flags.trust {
		return trust.Approve(tsrc, time.Now())
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("bundle %s (%s) hasn't been approved — review it, then pass --trust", tsrc.Location, shortPin(tsrc.Pin))
	}
	summary := labRiskSummary(cfg)
	summary = append(summary, fmt.Sprintf("loads %d image(s) into your container engine", len(m.Images)))
	if !confirmTrust(os.Stdin, os.Stdout, tsrc, status, prev, summary) {
		return errors.New("not trusted — nothing was loaded")
	}
	return trust.Approve(tsrc, time.Now())
}

func mustAbs(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func newBundleInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <bundle.tar.gz>",
		Short: "Show what's in a bundle (verifies it, installs nothing)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tmp, err := os.MkdirTemp("", "astrona-inspect-*")
			if err != nil {
				return err
			}
			defer os.RemoveAll(tmp)
			m, err := bundle.Extract(args[0], tmp)
			if err != nil {
				return fmt.Errorf("invalid bundle: %w", err)
			}
			printBundleManifest(os.Stdout, m)
			return nil
		},
	}
}

func printBundleManifest(w io.Writer, m *bundle.Manifest) {
	fmt.Fprintf(w, "Lab:        %s\nArch:       %s\nCreated:    %s by astrona %s\nNode image: %s\n\nImages (checksums verified):\n",
		m.Lab, m.Arch, m.CreatedAt.Format(time.RFC3339), m.CreatedBy, m.NodeImage)
	for _, im := range m.Images {
		fmt.Fprintf(w, "  %-8s %s\n", im.Purpose, im.Ref)
	}
	if len(m.AddonManifests) > 0 {
		fmt.Fprintf(w, "\nAddon manifests:\n")
		for _, am := range m.AddonManifests {
			fmt.Fprintf(w, "  %s (sha256:%s)\n", am.Addon, am.SHA256[:12])
		}
	}
}
