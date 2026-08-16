package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// QEMUImageSource describes the VM base image: a local qcow2 file, a URL to
// download one, or an OCI artifact to pull one from. Checksum verification
// (Checksum/Checksums, below) is optional — a maintainer choice, not a
// technical necessity: a VM base image is a full bootable OS, so booting one
// unverified means trusting whatever the source currently serves with no
// protection against it changing later (registries/URLs are mutable). If
// you skip it, acquireBaseImage still warns on every run, but does cache the
// image (keyed by source, not content) — a cheap best-effort freshness check
// (HTTP ETag/Last-Modified for "url", the OCI manifest digest for "oci")
// decides whether to reuse the cached copy or re-fetch, and a failed
// freshness check (e.g. offline) falls back to the cached copy rather than
// erroring, with a warning either way. See unverifiedCacheHit.
type QEMUImageSource struct {
	Type string `yaml:"type"` // "file", "url", or "oci" (pulled via `oras pull`, e.g. a ghcr.io package)
	// Source and File may each contain the literal placeholder "{ARCH}",
	// substituted (by acquireBaseImage) with the resolved guest arch's OCI
	// spelling — "amd64" or "arm64" (see ociArchName), never astrona's own
	// "x86_64"/"aarch64" — before use. The resolved arch is QEMUConfig.Arch
	// if set, otherwise this host's own architecture (see normalizeArch).
	// This is what lets one lab config pull the right image on both an
	// arm64 dev laptop and an amd64 CI runner without editing anything.
	Source string `yaml:"source"`
	// Checksum ("sha256:<hex>"), when set, is verified against the exact
	// bytes booted: for "file"/"url" that's the whole referenced file; for
	// "oci" it's specifically the single extracted *.qcow2 (via File,
	// below) — never a hash of the manifest, the whole artifact/tarball, or
	// any other layer in it. For an "oci" source this is conveniently the
	// same digest `oras manifest fetch <ref>` prints for that layer (OCI
	// blobs are content-addressed by this exact digest), so it can be
	// copied directly from there rather than downloaded-and-hashed by hand.
	//
	// At most one of Checksum or Checksums may be set — never both; leaving
	// both unset is allowed (see QEMUImageSource's doc comment) but treated
	// differently from Checksums being set with a missing arch entry, which
	// is still a hard error (resolveChecksum). Checksums exists because a
	// single Checksum can't be correct for more than one resolved image: use
	// it (keyed by the same "amd64"/"arm64" spelling {ARCH} resolves to)
	// whenever Source/File is arch-templated, since a different arch is a
	// genuinely different file with a genuinely different hash.
	Checksum  string            `yaml:"checksum"`
	Checksums map[string]string `yaml:"checksums"`
	// File selects which *.qcow2 to use when an "oci" artifact contains more
	// than one (e.g. multiple build variants pushed under the same tag).
	// Matched against the pulled file's base name; ignored for "file"/"url"
	// sources. Optional: if omitted and the artifact has exactly one
	// *.qcow2, that one is used; if omitted and it has several, a file
	// named exactly "image.qcow2" is used if present (defaultOCIImageFile —
	// the convention this repo's own published images follow), otherwise
	// findQcow2InPull errors out and File becomes required to disambiguate.
	// May also contain "{ARCH}" — see Source.
	File string `yaml:"file"`
}

// QEMUExtraDisk describes one additional blank disk attached alongside the
// main (base-image-backed) disk — e.g. a scratch disk for practicing
// partitioning, LVM, or filesystem formatting without touching the VM's
// root filesystem. Unlike the main disk, an extra disk has no base image to
// size or format itself from, so SizeGB is required and it always starts
// unformatted (raw zeroed blocks) — whatever the lab's bootstrap/validation
// scripts do to it (fdisk, pvcreate, mkfs.ext4, ...) is up to the lab, not
// astrona. Disposable like the main overlay disk: recreated blank on every
// `astrona run`, deleted with the rest of the VM's state dir on
// `astrona destroy`.
type QEMUExtraDisk struct {
	SizeGB int `yaml:"sizeGB"` // required, > 0 — no base image to inherit a size from
	// Format is the qcow2/raw image the disk file itself is stored as on the
	// host — not a guest filesystem. "" => "qcow2" (default, sparse/thin on
	// the host); "raw" pre-allocates SizeGB up front. Either way the guest
	// sees an entirely blank block device with no partition table.
	Format string `yaml:"format"`
	// Serial, when set, is exposed to the guest as the virtio-blk device's
	// serial number — lets a bootstrap/validation script target a stable
	// path like /dev/disk/by-id/virtio-<serial> instead of relying on
	// /dev/vdb-style enumeration order, which isn't guaranteed stable across
	// multiple extra disks.
	Serial string `yaml:"serial"`
}

// qemuExtraDiskSpec is the resolved (defaulted, validated, on-disk) form of
// a QEMUExtraDisk that buildQEMUArgs consumes — mirrors how overlayPath is
// the resolved form of QEMUConfig.Image/DiskSizeGB.
type qemuExtraDiskSpec struct {
	Path   string
	Format string
	Serial string
}

// QEMUConfig is the qemu-specific block of a lab's runtime config.
// QEMUConfig is what CreateQEMUVM needs to boot exactly one VM — deliberately
// unaware of lab orchestration concepts like naming-for-humans or bootstrap/
// validation scripts. QEMUVM (config.go), one entry in a lab's
// runtime.qemu list, carries those and converts to this via asQEMUConfig.
type QEMUConfig struct {
	Image      QEMUImageSource `yaml:"image"`
	Arch       string          `yaml:"arch"`       // "" => this host's own architecture (see normalizeArch) | "x86_64"/"amd64" | "aarch64"/"arm64" (needs UEFI firmware installed, see locateAArch64Firmware)
	CPUs       int             `yaml:"cpus"`       // 0 => default 2
	MemoryMB   int             `yaml:"memoryMB"`   // 0 => default 2048
	DiskSizeGB int             `yaml:"diskSizeGB"` // 0 => use base image's own size
	// ExtraDisks attaches additional blank disks (/dev/vdb, /dev/vdc, ... in
	// the guest, in list order) alongside the main disk — see QEMUExtraDisk.
	ExtraDisks []QEMUExtraDisk `yaml:"extraDisks"`
	SSHPort    int             `yaml:"sshPort"` // 0 => auto-pick a free host port
	// Display, when true, opens qemu's normal GUI window (e.g. a desktop
	// guest you want to actually look at) instead of running headless. Either
	// way CreateQEMUVM never blocks the CLI and the VM keeps running in the
	// background after `astrona run` returns — see CreateQEMUVM for why the
	// two modes launch qemu differently under the hood. Default false
	// (headless) — right for the SSH-only labs most of this repo's examples
	// are.
	Display         bool `yaml:"display"`
	SSHPasswordAuth bool `yaml:"sshPasswordAuth"` // If true, cloud-init enables password SSH authentication (ssh_pwauth: true)
}

// QEMUHandle is what a running VM looks like to the rest of the CLI: enough
// to SSH in and to tear it down later, possibly from a separate process
// invocation (astrona run, then later astrona submit/destroy). It is persisted to
// StateDir/handle.json because — unlike a kind cluster, whose state is owned
// by the container engine and queryable by name from any process — a raw
// qemu process has no such registry of its own.
type QEMUHandle struct {
	ClusterName string
	PID         int
	SSHHost     string
	SSHPort     int
	SSHUser     string
	SSHKeyPath  string
	KnownHosts  string
	StateDir    string
	StartedAt   time.Time // used by `astrona list` to report uptime
}

const qemuSSHUser = "student"

// maxQEMUImageDownloadBytes bounds a downloaded VM base image. Generous
// compared to maxScriptDownloadBytes since real cloud images run several
// GB, but still bounded rather than unlimited — checksum verification
// happens after the download completes, so this caps how much an
// untrusted URL can make the CLI write to disk before that check runs.
const maxQEMUImageDownloadBytes = 20 * 1024 * 1024 * 1024

// qemuBaseDir returns (creating if needed) ~/.astrona/qemu — the parent of
// every lab's per-VM state dir. A visible dotdir under $HOME rather than
// os.UserCacheDir() (macOS: ~/Library/Caches/astrona, easy to lose track of
// or have silently swept by a cache-cleaning tool) since this holds live,
// load-bearing state — a running VM's ephemeral SSH key and its only handle
// — not disposable cache data. Also what `astrona list` scans to enumerate
// every qemu lab, running or not.
func qemuBaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}

	dir := filepath.Join(home, ".astrona", "qemu")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create qemu base dir '%s': %w", dir, err)
	}

	return dir, nil
}

// qemuStateDir returns (creating if needed) the per-lab directory that holds
// everything about one qemu VM: overlay disk, cloud-init seed, ephemeral SSH
// key, known_hosts, pidfile, and the persisted QEMUHandle. 0700 because it
// holds an SSH private key.
func qemuStateDir(clusterName string) (string, error) {
	base, err := qemuBaseDir()
	if err != nil {
		return "", err
	}

	dir := filepath.Join(base, clusterName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create qemu state dir '%s': %w", dir, err)
	}

	return dir, nil
}

// normalizeArch maps the handful of spellings a lab author might write to
// the arch name qemu-system-<arch> and Go's runtime.GOARCH both use. An
// empty arch (QEMUConfig.Arch not set at all) resolves to *this host's own*
// architecture — via runtime.GOARCH, which already uses the "amd64"/"arm64"
// spelling this recurses into — rather than a fixed default, so an
// unspecified arch boots natively-accelerated (hvf/kvm) on whichever
// machine astrona happens to run on instead of silently defaulting to
// x86_64 and falling back to slow TCG emulation on an arm64 dev machine.
func normalizeArch(arch string) string {
	switch strings.ToLower(arch) {
	case "":
		return normalizeArch(runtime.GOARCH)
	case "x86_64", "amd64":
		return "x86_64"
	case "aarch64", "arm64":
		return "aarch64"
	default:
		return strings.ToLower(arch)
	}
}

// ociArchName maps astrona's internal qemu arch spelling (normalizeArch's
// output: "x86_64"/"aarch64") to the "amd64"/"arm64" spelling OCI
// registries, image tags, and Docker/Go tooling actually use. This is what
// the "{ARCH}" template variable in QEMUImageSource.Source/File resolves
// to — never astrona's own "x86_64"/"aarch64" spelling.
func ociArchName(normalizedArch string) string {
	switch normalizedArch {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	default:
		return normalizedArch
	}
}

// DetectQEMUBinary finds qemu-system-<arch> on PATH for the requested guest
// architecture.
func DetectQEMUBinary(arch string) (string, error) {
	bin := "qemu-system-" + normalizeArch(arch)

	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH: %w", bin, err)
	}

	return path, nil
}

// DetectAccelerator picks the fastest available accelerator for the
// requested guest arch on this host: HVF on macOS or KVM on Linux, but only
// when the guest arch matches the host arch (hardware acceleration cannot
// cross architectures) — otherwise falls back to the TCG software emulator
// and prints a warning, since that's a real, user-visible performance cliff.
func DetectAccelerator(arch string) string {
	target := normalizeArch(arch)
	hostArch := normalizeArch(runtime.GOARCH)

	if target != hostArch {
		fmt.Printf("[WARN] guest arch '%s' differs from host arch '%s': falling back to software emulation (tcg), this will be slow\n", target, hostArch)
		return "tcg"
	}

	switch runtime.GOOS {
	case "darwin":
		return "hvf"
	case "linux":
		if _, err := os.Stat("/dev/kvm"); err == nil {
			return "kvm"
		}
		fmt.Printf("[WARN] /dev/kvm not available: falling back to software emulation (tcg), this will be slow\n")
		return "tcg"
	default:
		fmt.Printf("[WARN] no known accelerator for this OS: falling back to software emulation (tcg), this will be slow\n")
		return "tcg"
	}
}

// resolveChecksum picks which checksum acquireBaseImage should verify
// against: the single Checksum field, or — when Source/File is
// arch-templated — Checksums keyed by ociArch. At most one of
// Checksum/Checksums may be set; if neither is set, resolveChecksum returns
// "" (no error) and acquireBaseImage boots the image unverified — see its
// doc comment for the tradeoff this accepts. If Checksums *is* set but has
// no entry for ociArch, that's still a hard error (not treated as "unset")
// — the lab author opted into per-arch pinning, just not for this one, and
// silently falling back to unverified there would be a more surprising
// failure mode than just saying so.
func resolveChecksum(img QEMUImageSource, ociArch string) (string, error) {
	hasSingle := strings.TrimSpace(img.Checksum) != ""
	hasMap := len(img.Checksums) > 0

	switch {
	case hasSingle && hasMap:
		return "", fmt.Errorf("qemu image '%s' sets both checksum and checksums — use exactly one", img.Source)
	case hasSingle:
		return img.Checksum, nil
	case hasMap:
		cs, ok := img.Checksums[ociArch]
		if !ok {
			keys := make([]string, 0, len(img.Checksums))
			for k := range img.Checksums {
				keys = append(keys, k)
			}
			return "", fmt.Errorf("qemu image '%s' has no checksums entry for arch '%s' (have: %v)", img.Source, ociArch, keys)
		}
		return cs, nil
	default:
		return "", nil
	}
}

// parseChecksum splits "sha256:<hex>" into its algorithm and hex digest.
// Only sha256 is supported today — deliberately not extensible to weaker
// algorithms.
func parseChecksum(checksum string) (algo, hexDigest string, err error) {
	parts := strings.SplitN(checksum, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid checksum format '%s': expected '<algo>:<hex>', e.g. 'sha256:abcd...'", checksum)
	}

	algo = strings.ToLower(parts[0])
	if algo != "sha256" {
		return "", "", fmt.Errorf("unsupported checksum algorithm '%s': only sha256 is supported", algo)
	}

	return algo, strings.ToLower(strings.TrimSpace(parts[1])), nil
}

// verifyChecksum reads path and compares its sha256 against expectedHex. It
// never mutates or deletes path itself — the caller decides what to clean up
// (a downloaded temp file vs. a user's own local image file).
func verifyChecksum(path, expectedHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open '%s' for checksum verification: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("failed to read '%s' for checksum verification: %w", path, err)
	}

	actualHex := hex.EncodeToString(h.Sum(nil))
	if actualHex != expectedHex {
		return fmt.Errorf("checksum mismatch for qemu base image '%s': expected %s, got %s (refusing to boot an unverified image)", path, expectedHex, actualHex)
	}

	return nil
}

// acquireBaseImage resolves a QEMUImageSource to a local file path (joining
// relative "file" sources against baseDir, downloading "url" sources via the
// same downloadToTemp helper scripts.go uses, or pulling "oci" sources via
// `oras pull`). The returned cleanup only ever removes a downloaded/pulled
// temp path that isn't also the persistent cache — a user's own local base
// image, and any image now living in the checksum cache, is never deleted,
// even on a checksum mismatch of some *other* file.
//
// Checksum verification (via resolveChecksum) is optional, not mandatory —
// a deliberate choice by this project's maintainer, made with the
// supply-chain tradeoff explained (a VM base image is a full bootable OS;
// an unverified one trusts whatever the source currently serves, with no
// protection against it changing later). An unset checksum prints a
// `[WARN]` every time rather than silently booting an unverified image with
// no trace. It's still cached — see unverifiedCacheHit — just keyed by
// source and kept fresh by a cheap online check instead of by content
// address, since there's no checksum to address by. Setting a checksum
// still gets exactly the same enforcement as before, cached the same way as
// always (content-addressed, re-verified on every hit).
func acquireBaseImage(img QEMUImageSource, baseDir, stateDir, hostArch string) (string, func(), error) {
	noopCleanup := func() {}

	ociArch := ociArchName(hostArch)
	source := strings.ReplaceAll(img.Source, "{ARCH}", ociArch)
	file := strings.ReplaceAll(img.File, "{ARCH}", ociArch)

	checksum, err := resolveChecksum(img, ociArch)
	if err != nil {
		return "", noopCleanup, err
	}

	verify := checksum != ""
	sourceType := strings.ToLower(img.Type)
	networkSourced := sourceType == "url" || sourceType == "oci"

	var algo, expectedHex string
	if verify {
		algo, expectedHex, err = parseChecksum(checksum)
		if err != nil {
			return "", noopCleanup, err
		}
	} else if networkSourced {
		fmt.Printf("[WARN] qemu image '%s' has no checksum set — booting it unverified. Set image.checksum or image.checksums to pin and verify it. Reusing/refreshing the local cache by best-effort online check instead (see `astrona images list`).\n", source)
	}

	if verify && networkSourced {
		if cachePath, ok, err := cacheHit(source, algo, expectedHex); err != nil {
			return "", noopCleanup, err
		} else if ok {
			fmt.Printf("Using cached qemu base image (%s:%s)\n", algo, expectedHex[:12])
			return cachePath, noopCleanup, nil
		}
	}

	// unverifiedMeta, when non-nil, means "fetch (or re-fetch) and cache
	// under these paths" — a nil hitPath alongside it means there was
	// nothing usable cached yet; a non-empty hitPath alongside it means
	// there was a cached copy but the freshness check says it's stale.
	// unverifiedMeta nil with hitPath set means the cached copy is either
	// still fresh or unreachable-but-present — either way, boot it as-is.
	var unverifiedMeta *imageCacheMeta
	var unverifiedDataPath, unverifiedMetaPath string
	if !verify && networkSourced {
		var hitPath string
		hitPath, unverifiedDataPath, unverifiedMetaPath, unverifiedMeta = unverifiedCacheHit(source, sourceType)
		if unverifiedMeta == nil {
			return hitPath, noopCleanup, nil
		}
	}

	var path string
	cleanup := noopCleanup

	switch sourceType {
	case "file":
		resolved, err := joinWithinBaseDir(baseDir, source)
		if err != nil {
			return "", noopCleanup, fmt.Errorf("failed to resolve qemu base image path: %w", err)
		}
		path = resolved
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return "", noopCleanup, fmt.Errorf("qemu base image does not exist: %s", path)
		}
	case "url":
		tmpPath, clean, err := downloadToTemp(source, "astrona-qemu-image-*.img", maxQEMUImageDownloadBytes)
		if err != nil {
			return "", noopCleanup, fmt.Errorf("failed to download qemu base image from %s: %w", source, err)
		}
		path = tmpPath
		cleanup = clean
	case "oci":
		tmpPath, clean, err := pullOCIImage(source, file, stateDir)
		if err != nil {
			return "", noopCleanup, err
		}
		path = tmpPath
		cleanup = clean
	default:
		return "", noopCleanup, fmt.Errorf("unsupported type '%s' for qemu image (must be 'file', 'url', or 'oci')", img.Type)
	}

	if verify {
		if err := verifyChecksum(path, expectedHex); err != nil {
			cleanup()
			return "", noopCleanup, err
		}
	}

	if verify && networkSourced {
		cachePath, err := cachedImagePath(source, algo, expectedHex)
		if err != nil {
			fmt.Printf("[WARN] could not resolve image cache dir, not caching this run: %s\n", err)
			return path, cleanup, nil
		}
		if err := persistToCache(path, cachePath); err != nil {
			fmt.Printf("[WARN] failed to cache downloaded qemu base image, will re-fetch next run: %s\n", err)
			return path, cleanup, nil
		}
		finalizeImageCacheMeta(cachePath, cachePath+".meta.json", &imageCacheMeta{
			Source:   source,
			Type:     sourceType,
			Verified: true,
			SHA256:   expectedHex,
		})
		cleanup() // now duplicated in the cache — the fetched temp copy is redundant
		return cachePath, noopCleanup, nil
	}

	if !verify && networkSourced && unverifiedMeta != nil {
		if err := persistToCache(path, unverifiedDataPath); err != nil {
			fmt.Printf("[WARN] failed to cache qemu base image, will re-fetch/re-check next run: %s\n", err)
			return path, cleanup, nil
		}
		if sum, err := sha256File(unverifiedDataPath); err == nil {
			unverifiedMeta.SHA256 = sum
		}
		finalizeImageCacheMeta(unverifiedDataPath, unverifiedMetaPath, unverifiedMeta)
		cleanup()
		return unverifiedDataPath, noopCleanup, nil
	}

	return path, cleanup, nil
}

// imageCacheDir returns (creating if needed) ~/.astrona/cache/images —
// where checksum-verified qemu base images from "url"/"oci" sources are
// cached, keyed by their own checksum (see acquireBaseImage).
func imageCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user home dir: %w", err)
	}

	dir := filepath.Join(home, ".astrona", "cache", "images")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create image cache dir '%s': %w", dir, err)
	}

	return dir, nil
}

// cacheSlug derives a filesystem-safe, human-readable prefix for a cached
// image's filename from its resolved source (the OCI ref or URL, with
// "{ARCH}" already substituted) — e.g.
// "ghcr.io/astrona-io/ubuntu-qcow2-image:24.04-base-arm64" becomes
// "ubuntu-qcow2-image-24.04-base-arm64", so `ls ~/.astrona/cache/images`
// shows what an image actually is instead of only a hash. Purely cosmetic:
// the hash suffix cachedImagePath appends is what actually identifies the
// entry — this never affects cache lookups' correctness, only readability.
func cacheSlug(resolvedSource string) string {
	slug := resolvedSource
	if i := strings.LastIndex(slug, "/"); i != -1 {
		slug = slug[i+1:]
	}

	var b strings.Builder
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}

	trimmed := strings.Trim(b.String(), "-")
	if trimmed == "" {
		return "image"
	}
	return trimmed
}

// cachedImagePath returns where a checksum-verified image would live in the
// cache: cacheSlug(resolvedSource), suffixed with a short hash so the entry
// is still uniquely keyed by content — two labs (or two revisions of one
// lab, or two differently-named sources that happen to resolve to the same
// bytes) never collide, even if their slug matches or their full digest
// does. The short hash is cosmetic-length only (12 hex chars, same as the
// "Using cached qemu base image" log line) — verifyChecksum always checks
// the full digest regardless of what's in the filename.
func cachedImagePath(resolvedSource, algo, hexDigest string) (string, error) {
	dir, err := imageCacheDir()
	if err != nil {
		return "", err
	}

	shortHash := hexDigest
	if len(shortHash) > 12 {
		shortHash = shortHash[:12]
	}

	name := fmt.Sprintf("%s-%s-%s.qcow2", cacheSlug(resolvedSource), algo, shortHash)
	return filepath.Join(dir, name), nil
}

// cacheHit checks whether expectedHex is already in the image cache and, if
// so, re-verifies it before trusting it — a corrupted or tampered cache
// entry is never silently booted, it's just treated as a miss (removed, so
// a slow re-fetch on this run is enough to fix it rather than every run).
func cacheHit(resolvedSource, algo, expectedHex string) (string, bool, error) {
	cachePath, err := cachedImagePath(resolvedSource, algo, expectedHex)
	if err != nil {
		return "", false, err
	}

	if _, err := os.Stat(cachePath); err != nil {
		return "", false, nil
	}

	if err := verifyChecksum(cachePath, expectedHex); err != nil {
		fmt.Printf("[WARN] cached qemu base image failed checksum verification, discarding and re-fetching: %s\n", err)
		os.Remove(cachePath)
		return "", false, nil
	}

	return cachePath, true, nil
}

// persistToCache copies a checksum-verified image into the cache at
// cachePath, writing to a same-directory temp file and renaming into place
// (rename is atomic within one directory) so a concurrent astrona
// invocation never observes a partially-written cache entry.
func persistToCache(srcPath, cachePath string) error {
	tmp, err := os.CreateTemp(filepath.Dir(cachePath), ".cache-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create cache temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once successfully renamed into place

	src, err := os.Open(srcPath)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("failed to open '%s' to cache it: %w", srcPath, err)
	}
	defer src.Close()

	if _, err := io.Copy(tmp, src); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to copy '%s' into cache: %w", srcPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to finalize cache file: %w", err)
	}

	if err := os.Rename(tmpPath, cachePath); err != nil {
		return fmt.Errorf("failed to move cache file into place: %w", err)
	}

	return nil
}

// imageCacheMeta is the sidecar (<entry>.meta.json, next to <entry>.qcow2 in
// imageCacheDir) astrona writes for every cache entry it creates — both the
// long-standing checksum-verified kind (Verified: true) and the
// source-keyed unverified kind this struct was added for (Verified: false).
// It exists purely for `astrona images list` and, for unverified entries,
// to remember what to compare a freshness check's result against next time
// (ETag/LastModified for "url", the manifest Digest for "oci"). It is never
// itself trusted for verification — verifyChecksum always re-reads the
// actual cached file for that; SHA256 here is informational only for the
// unverified path (still checksum-derived and enforced on the verified
// path, same as always).
type imageCacheMeta struct {
	Source       string    `json:"source"`
	Type         string    `json:"type"`
	Verified     bool      `json:"verified"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"lastModified,omitempty"`
	Digest       string    `json:"digest,omitempty"`
	SHA256       string    `json:"sha256,omitempty"`
	SizeBytes    int64     `json:"sizeBytes,omitempty"`
	CachedAt     time.Time `json:"cachedAt"`
}

// loadImageCacheMeta reads a cache entry's sidecar metadata file. A missing
// file is not an error (nil, nil) — a cache entry can predate this field, or
// its own metadata write can have failed on a previous run without that
// being fatal to the cache entry itself (see finalizeImageCacheMeta).
func loadImageCacheMeta(metaPath string) (*imageCacheMeta, error) {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read image cache metadata '%s': %w", metaPath, err)
	}

	var m imageCacheMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse image cache metadata '%s': %w", metaPath, err)
	}
	return &m, nil
}

// saveImageCacheMeta writes meta to metaPath via the same write-to-temp-then-
// rename pattern persistToCache uses for the image data itself, so a
// concurrent astrona invocation never observes a half-written metadata file.
func saveImageCacheMeta(metaPath string, meta *imageCacheMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode image cache metadata: %w", err)
	}

	tmp := metaPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("failed to write image cache metadata: %w", err)
	}
	if err := os.Rename(tmp, metaPath); err != nil {
		return fmt.Errorf("failed to move image cache metadata into place: %w", err)
	}
	return nil
}

// finalizeImageCacheMeta fills in the fields only known once dataPath exists
// on disk (its size, and "now" as the cache time) and saves meta to
// metaPath. Best-effort by design, matching persistToCache's own
// error-handling: a metadata write failure only degrades `astrona images
// list`'s output for this entry, it never invalidates the cached image data
// itself, so it's logged and swallowed rather than propagated.
func finalizeImageCacheMeta(dataPath, metaPath string, meta *imageCacheMeta) {
	if info, err := os.Stat(dataPath); err == nil {
		meta.SizeBytes = info.Size()
	}
	meta.CachedAt = time.Now()

	if err := saveImageCacheMeta(metaPath, meta); err != nil {
		fmt.Printf("[WARN] failed to save image cache metadata: %s\n", err)
	}
}

// sha256File hashes path's contents, returning the lowercase hex digest.
// Used to record an unverified cache entry's own content hash for display
// (imageCacheMeta.SHA256) — informational only, never re-checked the way a
// verified entry's checksum is by cacheHit.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open '%s' to hash it: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to read '%s' to hash it: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// unverifiedCacheKey derives a short, stable, content-independent id from
// resolvedSource alone — there's no checksum to content-address this cache
// entry by (that's the whole reason it exists: acquireBaseImage's
// unverified path), so identity is the source string itself.
func unverifiedCacheKey(resolvedSource string) string {
	sum := sha256.Sum256([]byte(resolvedSource))
	return hex.EncodeToString(sum[:])[:12]
}

// unverifiedCachePaths returns where a source-keyed (checksum-less) cache
// entry for resolvedSource would live: its image data and its metadata
// sidecar. Suffixed "-unverified-" so these entries are visually distinct
// from checksum-keyed ones (cachedImagePath) when browsing the cache dir by
// hand, and so the two naming schemes can never collide.
func unverifiedCachePaths(resolvedSource string) (dataPath, metaPath string, err error) {
	dir, err := imageCacheDir()
	if err != nil {
		return "", "", err
	}

	base := fmt.Sprintf("%s-unverified-%s", cacheSlug(resolvedSource), unverifiedCacheKey(resolvedSource))
	dataPath = filepath.Join(dir, base+".qcow2")
	metaPath = filepath.Join(dir, base+".meta.json")
	return dataPath, metaPath, nil
}

// freshnessCheckTimeout bounds how long acquireBaseImage waits on a
// best-effort online freshness check (an HTTPS HEAD for "url", `oras
// manifest fetch --descriptor` for "oci") before treating the host as
// offline and falling back to whatever's cached — short on purpose, since
// an unreachable host should fail fast into that fallback rather than make
// every `astrona run` hang for the full download timeout just to find out.
const freshnessCheckTimeout = 15 * time.Second

// checkURLFreshness performs a lightweight HTTPS HEAD request — never a GET
// — to compare against a cached entry's stored ETag/Last-Modified without
// downloading the (potentially multi-GB) body. fresh is only ever true when
// prior is non-nil and at least one of ETag/Last-Modified matches what the
// server reports now; a source with neither header (some plain static file
// servers) can never report fresh — every run treats it as changed and
// re-downloads, which is correct given astrona has no other signal to trust.
func checkURLFreshness(source string, prior *imageCacheMeta) (fresh bool, etag, lastModified string, err error) {
	if !strings.HasPrefix(source, "https://") {
		return false, "", "", fmt.Errorf("refusing to check non-https URL '%s': only https:// sources are allowed", source)
	}

	ctx, cancel := context.WithTimeout(context.Background(), freshnessCheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, source, nil)
	if err != nil {
		return false, "", "", fmt.Errorf("failed to build freshness check request for '%s': %w", source, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, "", "", fmt.Errorf("failed to reach '%s' to check for updates: %w", source, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", "", fmt.Errorf("server returned status checking '%s': %s", source, resp.Status)
	}

	etag = resp.Header.Get("ETag")
	lastModified = resp.Header.Get("Last-Modified")

	if prior != nil {
		if etag != "" && prior.ETag != "" {
			fresh = etag == prior.ETag
		} else if lastModified != "" && prior.LastModified != "" {
			fresh = lastModified == prior.LastModified
		}
	}

	return fresh, etag, lastModified, nil
}

// orasDescriptor is the subset of `oras manifest fetch --descriptor`'s JSON
// output checkOCIFreshness needs. That command fetches only the manifest's
// own descriptor (a few hundred bytes: digest, size, mediaType) rather than
// the manifest body or any blob/layer — the cheapest way to learn "has this
// ref moved" ORAS offers, deliberately not a full `oras pull`.
type orasDescriptor struct {
	Digest string `json:"digest"`
}

// checkOCIFreshness resolves ref's current manifest digest via `oras
// manifest fetch --descriptor` and compares it against prior's stored
// digest. fresh is only true when prior is non-nil and its digest matches.
func checkOCIFreshness(ref string, prior *imageCacheMeta) (fresh bool, digest string, err error) {
	orasPath, lookErr := exec.LookPath("oras")
	if lookErr != nil {
		return false, "", fmt.Errorf("oras not found in PATH (required to check qemu image type 'oci' for updates): %w", lookErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), freshnessCheckTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, orasPath, "manifest", "fetch", "--descriptor", ref).Output()
	if err != nil {
		return false, "", fmt.Errorf("failed to resolve manifest digest for '%s': %w", ref, err)
	}

	var desc orasDescriptor
	if err := json.Unmarshal(out, &desc); err != nil {
		return false, "", fmt.Errorf("failed to parse oras manifest descriptor for '%s': %w", ref, err)
	}
	if desc.Digest == "" {
		return false, "", fmt.Errorf("oras returned no digest for '%s'", ref)
	}

	if prior != nil {
		fresh = desc.Digest == prior.Digest
	}

	return fresh, desc.Digest, nil
}

// unverifiedCacheHit is acquireBaseImage's decision point for a
// checksum-less "url"/"oci" image source: reuse the cache, or fetch (and
// cache) again. It never errors — a freshness check failure (offline,
// timeout, source briefly unreachable) is exactly the case this exists to
// tolerate, not a reason to abort `astrona run`.
//
// Returns either:
//   - a non-empty hitPath and a nil meta: boot hitPath as-is (fresh cache
//     hit, or a stale/unreachable check with a cached copy to fall back on).
//   - an empty hitPath and a non-nil meta: nothing usable is cached (or the
//     cache is confirmed stale); acquireBaseImage should fetch normally and
//     then persist the result at dataPath/metaPath using the returned meta
//     (pre-filled with whatever ETag/Last-Modified/Digest this check learned,
//     so that gets recorded even though this run still had to fetch).
func unverifiedCacheHit(source, sourceType string) (hitPath, dataPath, metaPath string, meta *imageCacheMeta) {
	dataPath, metaPath, err := unverifiedCachePaths(source)
	if err != nil {
		fmt.Printf("[WARN] could not resolve image cache dir, not caching this run: %s\n", err)
		return "", "", "", nil
	}

	_, statErr := os.Stat(dataPath)
	haveCache := statErr == nil

	prior, loadErr := loadImageCacheMeta(metaPath)
	if loadErr != nil {
		fmt.Printf("[WARN] could not read image cache metadata, treating as uncached: %s\n", loadErr)
		prior = nil
	}

	var fresh bool
	var etag, lastModified, digest string
	var checkErr error
	if sourceType == "url" {
		fresh, etag, lastModified, checkErr = checkURLFreshness(source, prior)
	} else {
		fresh, digest, checkErr = checkOCIFreshness(source, prior)
	}

	next := &imageCacheMeta{
		Source:       source,
		Type:         sourceType,
		ETag:         etag,
		LastModified: lastModified,
		Digest:       digest,
	}

	switch {
	case checkErr != nil && haveCache:
		fmt.Printf("[WARN] could not check '%s' for updates (%s) — using cached qemu base image: %s\n", source, checkErr, dataPath)
		return dataPath, "", "", nil
	case checkErr != nil:
		fmt.Printf("[WARN] could not check '%s' for updates (%s), no local cache — fetching now\n", source, checkErr)
		return "", dataPath, metaPath, next
	case fresh && haveCache:
		fmt.Printf("Using cached qemu base image (unverified, up to date as of last check): %s\n", dataPath)
		return dataPath, "", "", nil
	default:
		if haveCache {
			fmt.Printf("Newer qemu base image available at '%s' — downloading and refreshing the cache\n", source)
		}
		return "", dataPath, metaPath, next
	}
}

// pullOCIImage pulls a qcow2 base image published as an OCI artifact (e.g. a
// ghcr.io package) via the `oras` CLI — the same shell-out-to-a-trusted-
// external-binary posture already given to kind/docker/qemu-img, since oras
// (not our own HTTP client) performs and controls the actual download. Only
// called on an image-cache miss (see acquireBaseImage) — a lab whose
// checksum is already cached never reaches this function.
// wantFile (QEMUImageSource.File) picks which *.qcow2 to use when the
// artifact contains more than one — see findQcow2InPull.
func pullOCIImage(ref, wantFile, stateDir string) (string, func(), error) {
	noopCleanup := func() {}

	orasPath, err := exec.LookPath("oras")
	if err != nil {
		return "", noopCleanup, fmt.Errorf("oras not found in PATH (required to pull qemu image type 'oci'): %w", err)
	}

	pullDir := filepath.Join(stateDir, "oci-image")
	os.RemoveAll(pullDir)
	if err := os.MkdirAll(pullDir, 0700); err != nil {
		return "", noopCleanup, fmt.Errorf("failed to create OCI pull dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(pullDir) }

	cmd := exec.Command(orasPath, "pull", ref, "-o", pullDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		cleanup()
		return "", noopCleanup, fmt.Errorf("failed to pull qemu base image '%s' via oras: %w", ref, err)
	}

	qcowPath, err := findQcow2InPull(pullDir, wantFile)
	if err != nil {
		cleanup()
		return "", noopCleanup, err
	}

	return qcowPath, cleanup, nil
}

// defaultOCIImageFile is the base name findQcow2InPull falls back to when
// image.file isn't set and the artifact has more than one *.qcow2 — the
// naming convention this repo expects published qcow2-base-image packages
// to follow: ship the canonical default under this exact name, any other
// file in the same artifact is a variant the lab author must opt into by
// name via image.file. Not a heuristic (never "pick the biggest one" or
// similar) — either this exact name exists, or findQcow2InPull still
// refuses to guess and asks for image.file.
const defaultOCIImageFile = "image.qcow2"

// findQcow2InPull walks dir (oras preserves each layer's title annotation as
// a relative path, e.g. "build/foo.qcow2", so this cannot assume a flat
// directory) for *.qcow2 files.
//
//   - wantFile set: must match exactly one file's base name — ambiguous or
//     absent matches are an error, never a guess.
//   - wantFile empty, exactly one *.qcow2 in the artifact: use it.
//   - wantFile empty, more than one: use defaultOCIImageFile if present;
//     otherwise error out listing candidates and asking for image.file.
func findQcow2InPull(dir, wantFile string) (string, error) {
	var matches []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".qcow2") {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to read pulled OCI artifact dir '%s': %w", dir, err)
	}

	if wantFile != "" {
		var want []string
		for _, m := range matches {
			if filepath.Base(m) == wantFile {
				want = append(want, m)
			}
		}
		switch len(want) {
		case 0:
			return "", fmt.Errorf("image.file '%s' not found in pulled OCI artifact (found: %v)", wantFile, relBaseNames(matches))
		case 1:
			return want[0], nil
		default:
			return "", fmt.Errorf("image.file '%s' matched multiple files in pulled OCI artifact: %v", wantFile, want)
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("pulled OCI artifact contained no .qcow2 file")
	case 1:
		return matches[0], nil
	default:
		for _, m := range matches {
			if filepath.Base(m) == defaultOCIImageFile {
				return m, nil
			}
		}
		return "", fmt.Errorf("pulled OCI artifact contains multiple .qcow2 files (%v) and none is named '%s' — set image.file to pick one", relBaseNames(matches), defaultOCIImageFile)
	}
}

// relBaseNames renders matches as base names for an error message.
func relBaseNames(matches []string) []string {
	names := make([]string, len(matches))
	for i, m := range matches {
		names[i] = filepath.Base(m)
	}
	return names
}

// createOverlayDisk creates a qcow2 overlay backed by basePath, so the base
// image is never opened for write by the running VM. basePath must be
// absolute — qcow2 backing-file references are resolved relative to the
// overlay's own directory otherwise, which breaks once the overlay and base
// don't live side by side (they don't: the overlay lives in stateDir).
func createOverlayDisk(basePath, stateDir string, diskSizeGB int) (string, error) {
	qemuImgPath, err := exec.LookPath("qemu-img")
	if err != nil {
		return "", fmt.Errorf("qemu-img not found in PATH: %w", err)
	}

	overlayPath := filepath.Join(stateDir, "overlay.qcow2")
	os.Remove(overlayPath)

	cmd := exec.Command(qemuImgPath, "create", "-f", "qcow2", "-F", "qcow2", "-b", basePath, overlayPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to create overlay disk: %w", err)
	}

	if diskSizeGB > 0 {
		baseSize, err := qemuImgVirtualSize(qemuImgPath, basePath)
		if err != nil {
			return "", err
		}

		requested := int64(diskSizeGB) * 1024 * 1024 * 1024
		if requested <= baseSize {
			fmt.Printf("[INFO] diskSizeGB %dG <= base image's own %.1fG, skipping resize (overlay already at least that big)\n", diskSizeGB, float64(baseSize)/(1024*1024*1024))
		} else {
			resizeCmd := exec.Command(qemuImgPath, "resize", overlayPath, fmt.Sprintf("%dG", diskSizeGB))
			resizeCmd.Stdout = os.Stdout
			resizeCmd.Stderr = os.Stderr
			if err := resizeCmd.Run(); err != nil {
				return "", fmt.Errorf("failed to resize overlay disk to %dG: %w", diskSizeGB, err)
			}
		}
	}

	return overlayPath, nil
}

// createExtraDisk creates one blank (no backing file) disk for cfg.ExtraDisks[index]
// — validated and defaulted by the caller — sized disk.SizeGB, in stateDir.
// Unlike createOverlayDisk there's no base image to back or resize against:
// the disk starts empty every time, same disposable-per-run posture as the
// main overlay disk.
func createExtraDisk(stateDir string, index int, format string, sizeGB int) (string, error) {
	qemuImgPath, err := exec.LookPath("qemu-img")
	if err != nil {
		return "", fmt.Errorf("qemu-img not found in PATH: %w", err)
	}

	path := filepath.Join(stateDir, fmt.Sprintf("extra-disk-%d.%s", index, format))
	os.Remove(path)

	cmd := exec.Command(qemuImgPath, "create", "-f", format, path, fmt.Sprintf("%dG", sizeGB))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to create extra disk %d (%dG, %s): %w", index, sizeGB, format, err)
	}

	return path, nil
}

// resolveExtraDisks validates and creates every disk in cfg.ExtraDisks,
// returning the resolved specs buildQEMUArgs needs.
func resolveExtraDisks(stateDir string, disks []QEMUExtraDisk) ([]qemuExtraDiskSpec, error) {
	specs := make([]qemuExtraDiskSpec, 0, len(disks))
	for i, d := range disks {
		if d.SizeGB <= 0 {
			return nil, fmt.Errorf("runtime.qemu extraDisks[%d]: sizeGB must be > 0 (got %d) — an extra disk has no base image to inherit a size from", i, d.SizeGB)
		}

		format := d.Format
		if format == "" {
			format = "qcow2"
		} else if format != "qcow2" && format != "raw" {
			return nil, fmt.Errorf("runtime.qemu extraDisks[%d]: format must be 'qcow2' or 'raw' (got %q)", i, d.Format)
		}

		path, err := createExtraDisk(stateDir, i, format, d.SizeGB)
		if err != nil {
			return nil, err
		}

		specs = append(specs, qemuExtraDiskSpec{Path: path, Format: format, Serial: d.Serial})
	}

	return specs, nil
}

// qemuImgVirtualSize returns basePath's virtual disk size in bytes (qcow2's
// declared capacity, not the sparse file size on disk) via `qemu-img info
// --output=json`, so createOverlayDisk can tell a genuine grow request
// (diskSizeGB > base) from a no-op/shrink one (base already that size or
// bigger) before ever calling `qemu-img resize` — resize refuses to shrink
// without --shrink, which would otherwise surface as an opaque exit-status-1.
func qemuImgVirtualSize(qemuImgPath, basePath string) (int64, error) {
	out, err := exec.Command(qemuImgPath, "info", "--output=json", basePath).Output()
	if err != nil {
		return 0, fmt.Errorf("failed to inspect base image '%s': %w", basePath, err)
	}

	var info struct {
		VirtualSize int64 `json:"virtual-size"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return 0, fmt.Errorf("failed to parse qemu-img info for '%s': %w", basePath, err)
	}

	return info.VirtualSize, nil
}

// generateEphemeralSSHKey creates a fresh ed25519 keypair in stateDir. Never
// reused across labs or runs — DestroyQEMUVM removes the whole state dir, so
// the key never outlives its VM.
func generateEphemeralSSHKey(stateDir string) (privKeyPath, pubKey string, err error) {
	privKeyPath = filepath.Join(stateDir, "id_ed25519")
	pubKeyPath := privKeyPath + ".pub"

	os.Remove(privKeyPath)
	os.Remove(pubKeyPath)

	keygenPath, err := exec.LookPath("ssh-keygen")
	if err != nil {
		return "", "", fmt.Errorf("ssh-keygen not found in PATH: %w", err)
	}

	cmd := exec.Command(keygenPath, "-t", "ed25519", "-N", "", "-f", privKeyPath, "-C", "astrona-lab")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("failed to generate ephemeral SSH key: %w", err)
	}

	if err := os.Chmod(privKeyPath, 0600); err != nil {
		return "", "", fmt.Errorf("failed to set permissions on SSH private key: %w", err)
	}

	pubBytes, err := os.ReadFile(pubKeyPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read generated SSH public key: %w", err)
	}

	return privKeyPath, strings.TrimSpace(string(pubBytes)), nil
}

// cloudInitHostname derives a valid cloud-init hostname/instance-id from a
// lab's clusterName (e.g. "qemu-basics-01") instead of a generic hardcoded
// stand-in, so a VM's identity inside the guest matches the lab that booted
// it. Lowercases and replaces anything outside [a-z0-9-] with '-', trimming
// to a conservative 63 chars (the DNS label limit cloud-init's hostname
// module ultimately writes to /etc/hostname).
func cloudInitHostname(clusterName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(clusterName) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}

	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "astrona-lab"
	}
	if len(name) > 63 {
		name = name[:63]
	}

	return name
}

// buildCloudInitSeed writes a NoCloud user-data/meta-data pair — creating an
// SSH user with pubKey as its credential (and optionally password auth enabled) —
// into a dedicated subdirectory (never the whole stateDir, which also holds
// the private key and disk images) and packs just those two files into a
// "cidata"-labeled ISO9660 image via whichever ISO tool is available.
func buildCloudInitSeed(stateDir, clusterName, pubKey string, passwordAuth bool) (string, error) {
	seedSrcDir := filepath.Join(stateDir, "cidata-src")
	if err := os.MkdirAll(seedSrcDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create cloud-init seed source dir: %w", err)
	}

	hostname := cloudInitHostname(clusterName)

	sshPwAuth := "false"
	lockPasswd := "true"
	if passwordAuth {
		sshPwAuth = "true"
		lockPasswd = "false"
	}

	userData := fmt.Sprintf(`#cloud-config
hostname: %s
users:
  - name: %s
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: %s
    ssh_authorized_keys:
      - %s
ssh_pwauth: %s
disable_root: true
`, hostname, qemuSSHUser, lockPasswd, pubKey, sshPwAuth)
	metaData := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", hostname, hostname)

	userDataPath := filepath.Join(seedSrcDir, "user-data")
	metaDataPath := filepath.Join(seedSrcDir, "meta-data")

	if err := os.WriteFile(userDataPath, []byte(userData), 0600); err != nil {
		return "", fmt.Errorf("failed to write cloud-init user-data: %w", err)
	}
	if err := os.WriteFile(metaDataPath, []byte(metaData), 0600); err != nil {
		return "", fmt.Errorf("failed to write cloud-init meta-data: %w", err)
	}

	isoPath := filepath.Join(stateDir, "seed.iso")
	os.Remove(isoPath)

	tool, args, err := isoBuildCommand(seedSrcDir, userDataPath, metaDataPath, isoPath)
	if err != nil {
		return "", err
	}

	cmd := exec.Command(tool, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to build cloud-init seed image: %w", err)
	}

	return isoPath, nil
}

// isoBuildCommand picks the first available ISO9660 tool. hdiutil (macOS
// builtin) takes a source directory rather than an explicit file list, which
// is exactly why buildCloudInitSeed uses a dedicated seedSrcDir containing
// only user-data/meta-data — pointing hdiutil at the whole stateDir would
// bundle the SSH private key and disk images into the seed ISO.
func isoBuildCommand(seedSrcDir, userDataPath, metaDataPath, isoPath string) (string, []string, error) {
	if path, err := exec.LookPath("mkisofs"); err == nil {
		return path, []string{"-output", isoPath, "-volid", "CIDATA", "-joliet", "-rock", userDataPath, metaDataPath}, nil
	}
	if path, err := exec.LookPath("genisoimage"); err == nil {
		return path, []string{"-output", isoPath, "-volid", "CIDATA", "-joliet", "-rock", userDataPath, metaDataPath}, nil
	}
	if path, err := exec.LookPath("xorriso"); err == nil {
		return path, []string{"-as", "genisoimage", "-output", isoPath, "-volid", "CIDATA", "-joliet", "-rock", userDataPath, metaDataPath}, nil
	}
	if path, err := exec.LookPath("hdiutil"); err == nil {
		return path, []string{"makehybrid", "-o", isoPath, "-iso", "-joliet", "-default-volume-name", "CIDATA", seedSrcDir}, nil
	}
	return "", nil, fmt.Errorf("no ISO build tool found in PATH (need one of: mkisofs, genisoimage, xorriso, hdiutil)")
}

// aarch64FirmwareCandidates are the well-known install locations for the
// edk2/AAVMF aarch64 UEFI "CODE" firmware image across package managers and
// OSes. -machine virt (used for aarch64 guests) has no legacy BIOS, so
// booting it requires this firmware regardless of guest OS.
var aarch64FirmwareCandidates = []string{
	"/opt/homebrew/share/qemu/edk2-aarch64-code.fd", // macOS, Homebrew on Apple Silicon
	"/usr/local/share/qemu/edk2-aarch64-code.fd",    // macOS, Homebrew on Intel
	"/usr/share/AAVMF/AAVMF_CODE.fd",                // Debian/Ubuntu (qemu-efi-aarch64)
	"/usr/share/qemu-efi-aarch64/QEMU_EFI.fd",       // older Debian/Ubuntu naming
	"/usr/share/edk2/aarch64/QEMU_EFI.fd",           // Fedora/RHEL (edk2-aarch64)
	"/usr/share/edk2-armvirt/aarch64/QEMU_EFI.fd",   // openSUSE
}

// locateAArch64Firmware finds the edk2/AAVMF aarch64 UEFI CODE image needed
// to boot -machine virt. Checked, in order: an explicit override
// (ASTRONA_QEMU_AARCH64_FIRMWARE), `brew --prefix qemu`'s share dir (covers
// non-default Homebrew prefixes), then a fixed list of common OS/package
// install paths.
func locateAArch64Firmware() (string, error) {
	if override := os.Getenv("ASTRONA_QEMU_AARCH64_FIRMWARE"); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("ASTRONA_QEMU_AARCH64_FIRMWARE is set to '%s' but it does not exist: %w", override, err)
		}
		return override, nil
	}

	if brewPrefix, err := exec.Command("brew", "--prefix", "qemu").Output(); err == nil {
		candidate := filepath.Join(strings.TrimSpace(string(brewPrefix)), "share", "qemu", "edk2-aarch64-code.fd")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	for _, candidate := range aarch64FirmwareCandidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("no aarch64 UEFI firmware found (tried %d common install paths) — install one (e.g. 'brew install qemu' on macOS, 'apt install qemu-efi-aarch64' on Debian/Ubuntu) or set ASTRONA_QEMU_AARCH64_FIRMWARE to its path", len(aarch64FirmwareCandidates))
}

// createUEFIVars makes a fresh, per-VM, writable UEFI variable store sized
// to match the read-only CODE firmware (pflash requires both halves to be
// the same size). Recreated on every CreateQEMUVM call, matching the
// disposable-overlay-disk pattern — VM state doesn't persist across `lab
// up` runs.
func createUEFIVars(codePath, stateDir string) (string, error) {
	info, err := os.Stat(codePath)
	if err != nil {
		return "", fmt.Errorf("failed to stat aarch64 firmware '%s': %w", codePath, err)
	}

	varsPath := filepath.Join(stateDir, "efi-vars.fd")
	f, err := os.OpenFile(varsPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to create UEFI vars file: %w", err)
	}
	defer f.Close()

	if err := f.Truncate(info.Size()); err != nil {
		return "", fmt.Errorf("failed to size UEFI vars file: %w", err)
	}

	return varsPath, nil
}

// pickFreePort asks the OS for an unused TCP port on localhost.
func pickFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("failed to pick a free port: %w", err)
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port, nil
}

// readPidfile polls for qemu's -pidfile to appear: with -daemonize, qemu
// forks into the background and writes the pidfile itself after
// backgrounding, so there's a short window after cmd.Run() returns before
// the file exists.
func readPidfile(path string, timeout time.Duration) (int, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil {
				return pid, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	return 0, fmt.Errorf("timed out waiting for pidfile '%s'", path)
}

// writeHandleState persists a QEMUHandle so a later, separate process
// invocation (astrona submit, astrona destroy) can rediscover this VM.
func writeHandleState(h *QEMUHandle) error {
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal qemu VM state: %w", err)
	}

	statePath := filepath.Join(h.StateDir, "handle.json")
	if err := os.WriteFile(statePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write qemu VM state file: %w", err)
	}

	return nil
}

// processAlive checks whether pid refers to a live process, without sending
// a real signal (signal 0 is the standard "does this pid exist" probe).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	return process.Signal(syscall.Signal(0)) == nil
}

// sshRun runs remoteCmd on the VM over SSH, used only for internal
// readiness polling (waitForSSHReady) — actual lab scripts run through
// SSHExecutor.
func sshRun(h *QEMUHandle, remoteCmd string) error {
	args := []string{
		"-i", h.SSHKeyPath,
		"-p", strconv.Itoa(h.SSHPort),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "UserKnownHostsFile=" + h.KnownHosts,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		fmt.Sprintf("%s@%s", h.SSHUser, h.SSHHost),
		remoteCmd,
	}

	return exec.Command("ssh", args...).Run()
}

// pollUntil retries check until it succeeds or timeout elapses, returning
// the last error on timeout.
func pollUntil(timeout time.Duration, check func() error) error {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		if err := check(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(2 * time.Second)
	}

	return lastErr
}

// waitForSSHReady blocks until the VM accepts SSH connections and cloud-init
// has finished provisioning it — the second gate requires the base image to
// actually have cloud-init installed, standard on official cloud images.
func waitForSSHReady(h *QEMUHandle, timeout time.Duration) error {
	if err := pollUntil(timeout, func() error { return sshRun(h, "true") }); err != nil {
		return fmt.Errorf("timed out waiting for SSH to become available on %s:%d: %w", h.SSHHost, h.SSHPort, err)
	}

	if err := pollUntil(timeout, func() error { return sshRun(h, "cloud-init status --wait") }); err != nil {
		return fmt.Errorf("timed out waiting for cloud-init to finish on %s:%d: %w", h.SSHHost, h.SSHPort, err)
	}

	return nil
}

// prepareAArch64Firmware locates the edk2/AAVMF UEFI firmware and creates
// this VM's writable vars file, returning the pflash -drive args
// CreateQEMUVM should append. Only called when the guest arch is aarch64
// (-machine virt has no legacy BIOS, so booting it needs this regardless of
// guest OS — see locateAArch64Firmware).
func prepareAArch64Firmware(stateDir string) ([]string, error) {
	firmwareCode, err := locateAArch64Firmware()
	if err != nil {
		return nil, err
	}

	varsPath, err := createUEFIVars(firmwareCode, stateDir)
	if err != nil {
		return nil, err
	}

	return []string{
		"-drive", "if=pflash,format=raw,readonly=on,file=" + firmwareCode,
		"-drive", "if=pflash,format=raw,file=" + varsPath,
	}, nil
}

// buildQEMUArgs assembles the qemu-system-* command-line flags from an
// already-prepared VM: overlay disk, cloud-init seed, ssh port forward, and
// (for aarch64) the UEFI pflash drives from prepareAArch64Firmware.
//
// display picks between two different launch shapes, not just a flag:
//   - headless (display=false): "-display none -daemonize -pidfile ...".
//     qemu forks itself into the background after initializing; CreateQEMUVM
//     recovers the PID by polling pidfilePath (see readPidfile).
//   - GUI (display=true): no "-display"/"-daemonize"/"-pidfile" at all — qemu
//     opens its normal platform window (cocoa/gtk/sdl) and CreateQEMUVM
//     backgrounds it itself instead (detached via SysProcAttr, PID taken
//     straight from cmd.Process.Pid). -daemonize forks *after* the GUI
//     toolkit has already initialized a window and event loop, which is
//     unsafe/flaky for at least Cocoa on macOS — so the GUI path never
//     daemonizes, it's detached the other way instead.
func buildQEMUArgs(processName, machineType, accel string, cpus, memoryMB int, firmwareArgs []string, overlayPath, seedPath string, extraDisks []qemuExtraDiskSpec, sshPort int, display bool, pidfilePath, consolePath string) []string {
	args := []string{
		"-name", processName,
		"-machine", machineType,
		"-accel", accel,
		"-cpu", "max",
		"-smp", strconv.Itoa(cpus),
		"-m", strconv.Itoa(memoryMB),
	}
	args = append(args, firmwareArgs...)
	args = append(args,
		"-drive", "file="+overlayPath+",if=virtio,format=qcow2",
		"-drive", "file="+seedPath+",if=virtio,format=raw,readonly=on",
	)
	// Unlike the main/seed disks above, extra disks are wired up as a
	// separate -drive (if=none, so qemu doesn't auto-attach a device for it)
	// plus an explicit -device virtio-blk-pci: "serial" isn't a block-format
	// option -drive's combined if=virtio shorthand accepts (qemu rejects it
	// with "Block format 'qcow2' does not support the option 'serial'"), it's
	// a property of the virtio-blk device itself.
	for i, d := range extraDisks {
		driveID := fmt.Sprintf("extradisk%d", i)
		args = append(args, "-drive", fmt.Sprintf("file=%s,if=none,id=%s,format=%s", d.Path, driveID, d.Format))
		deviceArg := "virtio-blk-pci,drive=" + driveID
		if d.Serial != "" {
			deviceArg += ",serial=" + d.Serial
		}
		args = append(args, "-device", deviceArg)
	}
	args = append(args,
		"-netdev", fmt.Sprintf("user,id=net0,hostfwd=tcp:127.0.0.1:%d-:22", sshPort),
		"-device", "virtio-net-pci,netdev=net0",
		"-serial", "file:"+consolePath,
	)

	if display {
		return args
	}

	return append(args, "-display", "none", "-daemonize", "-pidfile", pidfilePath)
}

// CreateQEMUVM boots a new VM for clusterName: acquires and checksum-verifies
// the base image, creates a disposable overlay disk, generates an ephemeral
// SSH key, builds a cloud-init seed, launches qemu-system-* detached in the
// background, and waits for it to become SSH-ready.
func CreateQEMUVM(clusterName, baseDir string, cfg *QEMUConfig) (*QEMUHandle, error) {
	// Refuse to launch a second VM on top of an already-running one: nothing
	// below this point checks for an existing process, so without this guard
	// a repeat `astrona run` (no `astrona destroy` in between) silently
	// orphans the previous qemu process — it keeps running, invisible to
	// `astrona list`/`astrona destroy` the moment handle.json is overwritten
	// with the new instance's PID/port, wasting host resources and (via
	// leftover known_hosts entries from a now-vanished VM) sometimes
	// surfacing later as a confusing "host key verification failed" on
	// `astrona ssh`.
	if existing, err := LoadQEMUHandle(clusterName); err == nil {
		return nil, fmt.Errorf("qemu VM for lab '%s' is already running (pid %d, ssh %s@%s:%d) — run 'astrona ssh' to connect to it or 'astrona destroy' to tear it down before starting a new one", clusterName, existing.PID, existing.SSHUser, existing.SSHHost, existing.SSHPort)
	}

	arch := normalizeArch(cfg.Arch)

	stateDir, err := qemuStateDir(clusterName)
	if err != nil {
		return nil, err
	}

	qemuPath, err := DetectQEMUBinary(arch)
	if err != nil {
		return nil, err
	}
	accel := DetectAccelerator(arch)

	basePath, baseCleanup, err := acquireBaseImage(cfg.Image, baseDir, stateDir, arch)
	if err != nil {
		return nil, err
	}
	defer baseCleanup()

	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path for base image: %w", err)
	}

	overlayPath, err := createOverlayDisk(absBase, stateDir, cfg.DiskSizeGB)
	if err != nil {
		return nil, err
	}

	extraDisks, err := resolveExtraDisks(stateDir, cfg.ExtraDisks)
	if err != nil {
		return nil, err
	}

	privKeyPath, pubKey, err := generateEphemeralSSHKey(stateDir)
	if err != nil {
		return nil, err
	}

	seedPath, err := buildCloudInitSeed(stateDir, clusterName, pubKey, cfg.SSHPasswordAuth)
	if err != nil {
		return nil, err
	}

	sshPort := cfg.SSHPort
	if sshPort == 0 {
		p, err := pickFreePort()
		if err != nil {
			return nil, err
		}
		sshPort = p
	}

	cpus := cfg.CPUs
	if cpus <= 0 {
		cpus = 2
	}
	memoryMB := cfg.MemoryMB
	if memoryMB <= 0 {
		memoryMB = 2048
	}

	pidfilePath := filepath.Join(stateDir, "qemu.pid")
	consolePath := filepath.Join(stateDir, "console.log")
	os.Remove(pidfilePath)

	machineType := "q35"
	var firmwareArgs []string

	if arch == "aarch64" {
		machineType = "virt"

		fwArgs, err := prepareAArch64Firmware(stateDir)
		if err != nil {
			return nil, err
		}
		firmwareArgs = fwArgs
	}

	// Prefixed only for the qemu process's own -name (what `ps`/the window
	// title show, so an astrona-managed VM is identifiable at the OS level
	// among any other qemu processes on the machine) — the same relationship
	// kind has between a cluster's plain name and its "kind-<name>"
	// kubeconfig context. Every astrona-facing name (this function's
	// clusterName param, `astrona list`/`astrona ssh`/`astrona destroy`,
	// QEMUHandle.ClusterName, the state dir) stays unprefixed.
	processName := "astrona-" + clusterName

	args := buildQEMUArgs(processName, machineType, accel, cpus, memoryMB, firmwareArgs, overlayPath, seedPath, extraDisks, sshPort, cfg.Display, pidfilePath, consolePath)

	fmt.Printf("Launching qemu VM '%s' (arch=%s accel=%s ssh-port=%d display=%v)...\n", clusterName, arch, accel, sshPort, cfg.Display)

	cmd := exec.Command(qemuPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	var pid int
	if cfg.Display {
		// Detach qemu into its own session so it isn't tied to astrona's
		// process group/controlling terminal — it must keep running (and
		// its window stay open) after `astrona run` returns, the same
		// non-blocking end result -daemonize gives the headless path.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to launch qemu VM: %w", err)
		}
		pid = cmd.Process.Pid
		if err := os.WriteFile(pidfilePath, []byte(strconv.Itoa(pid)), 0600); err != nil {
			return nil, fmt.Errorf("failed to write qemu pidfile: %w", err)
		}
	} else {
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("failed to launch qemu VM: %w", err)
		}
		p, err := readPidfile(pidfilePath, 5*time.Second)
		if err != nil {
			return nil, fmt.Errorf("qemu started but pidfile was not written: %w", err)
		}
		pid = p
	}

	handle := &QEMUHandle{
		ClusterName: clusterName,
		PID:         pid,
		SSHHost:     "127.0.0.1",
		SSHPort:     sshPort,
		SSHUser:     qemuSSHUser,
		SSHKeyPath:  privKeyPath,
		KnownHosts:  filepath.Join(stateDir, "known_hosts"),
		StateDir:    stateDir,
		StartedAt:   time.Now(),
	}

	if err := writeHandleState(handle); err != nil {
		return nil, err
	}

	fmt.Printf("Waiting for VM to become SSH-ready...\n")
	if err := waitForSSHReady(handle, 3*time.Minute); err != nil {
		return handle, fmt.Errorf("VM launched but did not become ready: %w", err)
	}

	return handle, nil
}

// LoadQEMUHandle rediscovers an already-running VM for clusterName from its
// persisted state — used by `astrona submit`/`astrona destroy` which run in
// a separate process from the `astrona run` that created the VM.
func LoadQEMUHandle(clusterName string) (*QEMUHandle, error) {
	stateDir, err := qemuStateDir(clusterName)
	if err != nil {
		return nil, err
	}

	statePath := filepath.Join(stateDir, "handle.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no running qemu VM found for lab '%s' — run 'astrona run' first", clusterName)
		}
		return nil, fmt.Errorf("failed to read qemu VM state: %w", err)
	}

	var h QEMUHandle
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("failed to parse qemu VM state: %w", err)
	}

	if !processAlive(h.PID) {
		return nil, fmt.Errorf("qemu VM for lab '%s' is not running (stale state, pid %d not found) — run 'astrona run' again", clusterName, h.PID)
	}

	return &h, nil
}

// DestroyQEMUVM tears down clusterName's VM: SIGTERM (falling back to a
// forceful kill), then removes the whole per-lab state dir (overlay disk,
// cloud-init seed, ephemeral SSH key — none of it should outlive the VM).
// A no-op, not an error, if no VM state is found — matches cmd_down.go's
// existing best-effort teardown posture for kind.
func DestroyQEMUVM(clusterName string) error {
	stateDir, err := qemuStateDir(clusterName)
	if err != nil {
		return err
	}

	statePath := filepath.Join(stateDir, "handle.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("No qemu VM state found for '%s', nothing to tear down.\n", clusterName)
			return nil
		}
		return fmt.Errorf("failed to read qemu VM state: %w", err)
	}

	var h QEMUHandle
	if err := json.Unmarshal(data, &h); err != nil {
		return fmt.Errorf("failed to parse qemu VM state: %w", err)
	}

	if h.PID > 0 {
		if process, err := os.FindProcess(h.PID); err == nil {
			_ = process.Signal(syscall.SIGTERM)

			for i := 0; i < 50; i++ {
				if !processAlive(h.PID) {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}

			if processAlive(h.PID) {
				_ = process.Kill()
			}
		}
	}

	if err := os.RemoveAll(stateDir); err != nil {
		return fmt.Errorf("failed to remove qemu state dir '%s': %w", stateDir, err)
	}

	fmt.Printf("qemu VM for '%s' torn down.\n", clusterName)
	return nil
}
