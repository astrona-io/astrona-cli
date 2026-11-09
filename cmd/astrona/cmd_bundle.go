package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"astrona/internal/apphome"
	"astrona/internal/authoring"
	"astrona/internal/bundle"
	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/trust"
	"astrona/internal/ui"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// bundleImagesSidecar, written into a loaded bundle's lab directory, lists
// the addon images that have to be loaded into the kind nodes.
const bundleImagesSidecar = labops.BundleImagesSidecar

// applyBundleImages adds a loaded bundle's addon images to the lab's
// preloadImages (labops.ApplyBundleImages).
func applyBundleImages(cfg *config.LabConfig, baseDir string) { labops.ApplyBundleImages(cfg, baseDir) }

// labPreloadImages is what to load into the lab's own nodes
// (labops.LabPreloadImages).
func labPreloadImages(cfg *config.LabConfig, baseDir string) []string {
	return labops.LabPreloadImages(cfg, baseDir)
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
			opts := authoring.BundleOptions{Output: out, NodeImage: nodeImage, CreatedBy: Version}
			if opts.NodeImage, err = authoring.PrepareBundle(cfg, baseDir, opts); err != nil {
				return err
			}

			rep, err := ui.New("bundle", cfg.Metadata.Name, ui.Options{Verbose: flags.verbose, Plain: flags.headless})
			if err != nil {
				return err
			}
			defer rep.Close()

			res, err := authoring.CreateBundle(rep, cfg, baseDir, opts.NodeImage, opts)
			if err != nil {
				return err
			}
			rep.Close()

			m := res.Manifest
			fmt.Printf("\nBundle %s — %s, %d image(s), %d addon manifest(s), %.0f MB, %s.\n", out, m.Lab, len(m.Images), len(m.AddonManifests), float64(res.Size)/(1<<20), m.Arch)
			fmt.Printf("On the offline machine: astrona bundle load %s\n", filepath.Base(out))
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "output", "o", "", "Bundle file to write (.tar.gz)")
	cmd.Flags().StringVar(&nodeImage, "node-image", "", "Node image to bundle when the lab doesn't pin runtime.kind.version/image")
	return cmd
}

// bundlesDir is ~/.astrona/bundles (authoring.BundlesDir).
func bundlesDir() (string, error) { return authoring.BundlesDir() }

func newBundleLoadCmd(flags *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "load <bundle.tar.gz>",
		Short: "Load an offline bundle: verify it, load its images, unpack the lab",
		Long: "Verify a bundle (every image and addon manifest against its recorded SHA-256, the " +
			"CPU architecture), load its images into Docker/Podman, seed the addon cache (only " +
			"manifests matching astrona's own pinned versions are accepted), and unpack the lab " +
			"into ~/" + apphome.Name() + "/bundles/. Then run it like any local lab, with no network.\n\n" +
			"A bundle is someone else's code: like a remote lab, you're shown what it will do and " +
			"asked once per bundle (--trust to approve without asking).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src := args[0]
			ob, err := authoring.OpenBundle(src)
			if err != nil {
				return err
			}
			defer ob.Close()

			src = mustAbs(src)
			if err := trustBundle(flags, src, ob.SHA256, ob.Config, ob.Manifest); err != nil {
				return err
			}

			rep, err := ui.New("bundle-load", ob.Manifest.Lab, ui.Options{Verbose: flags.verbose, Plain: flags.headless})
			if err != nil {
				return err
			}
			defer rep.Close()

			dest, err := ob.Install(rep)
			if err != nil {
				return err
			}
			rep.Close()
			fmt.Printf("\nLoaded %s into %s — works without internet now:\n  astrona run -c %s\n", ob.Manifest.Lab, dest, dest)
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
	if flags.headless || !isatty.IsTerminal(os.Stdin.Fd()) {
		return fmt.Errorf("bundle %s (%s) hasn't been approved — review it, then pass --trust", tsrc.Location, shortPin(tsrc.Pin))
	}
	summary := labRiskSummary(cfg)
	summary = append(summary, fmt.Sprintf("loads %d image(s) into your container engine", len(m.Images)))
	if !confirmTrust(os.Stdin, promptOut, tsrc, status, prev, summary) {
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
			m, err := authoring.InspectBundle(args[0])
			if err != nil {
				return err
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
