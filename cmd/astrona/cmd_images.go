package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"astrona/internal/hypervisor"

	"github.com/spf13/cobra"
)

// newImagesCmd builds `astrona images`, a read-only inspection command for
// ~/.astrona/cache/images — the qemu base image cache acquireBaseImage
// populates (see hypervisor.go). Its own subcommand namespace (rather than a
// flat root verb like list/ssh/check) since it's the first command with more
// than one action worth grouping under a noun.
func newImagesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "images",
		Short: "Inspect astrona's local qemu base image cache",
	}
	cmd.AddCommand(newImagesListCmd())
	return cmd
}

func newImagesListCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List cached qemu base images (~/.astrona/cache/images)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			entries, err := listImageCache()
			if err != nil {
				return err
			}
			printImageCacheTable(entries)
			return nil
		},
	}
}

// imageCacheEntry is one row of `astrona images list` — a cached *.qcow2 in
// ImageCacheDir plus whatever its sidecar ImageCacheMeta could tell us about
// it. legacy is true for a *.qcow2 with no (or unreadable) sidecar — only
// possible for a cache entry written before finalizeImageCacheMeta existed,
// or one whose metadata write previously failed; the image data itself is
// unaffected either way, `astrona images list` just has less to say about it.
type imageCacheEntry struct {
	file      string
	source    string
	imageType string
	verified  bool
	legacy    bool
	digest    string
	sizeBytes int64
	cachedAt  time.Time
}

// listImageCache walks ImageCacheDir for *.qcow2 entries and pairs each with
// its <name>.qcow2.meta.json (verified entries) or <name>.meta.json
// (unverified entries — see unverifiedCachePaths) sidecar, falling back to a
// legacy row (file stat only) when no sidecar is present or it fails to
// parse.
func listImageCache() ([]imageCacheEntry, error) {
	dir, err := hypervisor.ImageCacheDir()
	if err != nil {
		return nil, err
	}

	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read image cache dir '%s': %w", dir, err)
	}

	var out []imageCacheEntry
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".qcow2") {
			continue
		}

		dataPath := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}

		entry := imageCacheEntry{
			file:      e.Name(),
			sizeBytes: info.Size(),
			cachedAt:  info.ModTime(),
			legacy:    true,
		}

		// Both naming schemes suffix the *.qcow2 base name with
		// ".meta.json" — cachedImagePath's "<slug>-<algo>-<hash>.qcow2"
		// (verified, see finalizeImageCacheMeta's cachePath+".meta.json"
		// call) and unverifiedCachePaths' "<slug>-unverified-<hash>.qcow2"
		// both just append it, so one lookup covers both.
		if meta, err := hypervisor.LoadImageCacheMeta(dataPath + ".meta.json"); err == nil && meta != nil {
			entry.source = meta.Source
			entry.imageType = meta.Type
			entry.verified = meta.Verified
			entry.legacy = false
			entry.sizeBytes = meta.SizeBytes
			if !meta.CachedAt.IsZero() {
				entry.cachedAt = meta.CachedAt
			}
			if meta.Verified {
				entry.digest = "sha256:" + meta.SHA256
			} else if meta.Digest != "" {
				entry.digest = meta.Digest
			} else if meta.SHA256 != "" {
				entry.digest = "sha256:" + meta.SHA256
			}
		}

		out = append(out, entry)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].file < out[j].file })
	return out, nil
}

// printImageCacheTable renders entries the same kubectl-get-style aligned
// table `astrona list` uses.
func printImageCacheTable(entries []imageCacheEntry) {
	if len(entries) == 0 {
		fmt.Println("No cached qemu base images (~/.astrona/cache/images is empty).")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 3, ' ', 0)
	fmt.Fprintln(w, "FILE\tTYPE\tVERIFIED\tSIZE\tCACHED\tSOURCE")
	for _, e := range entries {
		verified := "no"
		switch {
		case e.legacy:
			verified = "unknown (legacy entry)"
		case e.verified:
			verified = "yes"
		}

		imageType := e.imageType
		if imageType == "" {
			imageType = "-"
		}

		source := e.source
		if source == "" {
			source = "-"
		}

		cached := "-"
		if !e.cachedAt.IsZero() {
			cached = e.cachedAt.Format("2006-01-02 15:04")
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			e.file, imageType, verified, formatBytes(e.sizeBytes), cached, source)
	}
	w.Flush()
}

// formatBytes renders n as the coarsest human unit that fits — base images
// run from tens of MB to tens of GB, so this always shows at most one
// non-trivial unit rather than a raw byte count.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
