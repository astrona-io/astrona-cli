package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNormalizeArch(t *testing.T) {
	cases := map[string]string{
		"x86_64":  "x86_64",
		"amd64":   "x86_64",
		"aarch64": "aarch64",
		"arm64":   "aarch64",
		"AARCH64": "aarch64",
	}

	for in, want := range cases {
		if got := normalizeArch(in); got != want {
			t.Errorf("normalizeArch(%q) = %q, want %q", in, got, want)
		}
	}

	// An unset arch resolves to *this host's own* architecture, not a fixed
	// default — see normalizeArch's doc comment.
	if got, want := normalizeArch(""), normalizeArch(runtime.GOARCH); got != want {
		t.Errorf("normalizeArch(\"\") = %q, want host arch %q", got, want)
	}
}

func TestOciArchName(t *testing.T) {
	cases := map[string]string{
		"x86_64":  "amd64",
		"aarch64": "arm64",
	}

	for in, want := range cases {
		if got := ociArchName(in); got != want {
			t.Errorf("ociArchName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCacheSlug(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/astrona-io/ubuntu-qcow2-image:24.04-base-arm64": "ubuntu-qcow2-image-24.04-base-arm64",
		"ghcr.io/astrona-io/ubuntu-24.04-server-docker:arm64":    "ubuntu-24.04-server-docker-arm64",
		"https://example.com/images/debian-12.qcow2":             "debian-12.qcow2",
		"":    "image",
		"///": "image",
	}

	for in, want := range cases {
		if got := cacheSlug(in); got != want {
			t.Errorf("cacheSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCachedImagePathIncludesSlugAndHash(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := cachedImagePath("ghcr.io/astrona-io/ubuntu-qcow2-image:24.04-base-arm64", "sha256", "015b0dd5ac43c07e2579c29af7858ce811d204986e399f736111c3c6cc48768f")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	name := filepath.Base(path)
	if !strings.HasPrefix(name, "ubuntu-qcow2-image-24.04-base-arm64-sha256-") {
		t.Errorf("got %q, want prefix ubuntu-qcow2-image-24.04-base-arm64-sha256-", name)
	}
	if !strings.HasSuffix(name, "015b0dd5ac43.qcow2") {
		t.Errorf("got %q, want 12-char hash suffix 015b0dd5ac43.qcow2", name)
	}
}

func TestResolveChecksum(t *testing.T) {
	t.Run("single checksum", func(t *testing.T) {
		got, err := resolveChecksum(QEMUImageSource{Checksum: "sha256:abc"}, "arm64")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "sha256:abc" {
			t.Errorf("got %q, want sha256:abc", got)
		}
	})

	t.Run("checksums map keyed by arch", func(t *testing.T) {
		img := QEMUImageSource{Checksums: map[string]string{"amd64": "sha256:amd", "arm64": "sha256:arm"}}
		got, err := resolveChecksum(img, "arm64")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "sha256:arm" {
			t.Errorf("got %q, want sha256:arm", got)
		}
	})

	t.Run("checksums map missing arch entry errors", func(t *testing.T) {
		img := QEMUImageSource{Checksums: map[string]string{"amd64": "sha256:amd"}}
		if _, err := resolveChecksum(img, "arm64"); err == nil {
			t.Fatal("expected error for missing arch entry")
		}
	})

	t.Run("both checksum and checksums errors", func(t *testing.T) {
		img := QEMUImageSource{Checksum: "sha256:abc", Checksums: map[string]string{"arm64": "sha256:arm"}}
		if _, err := resolveChecksum(img, "arm64"); err == nil {
			t.Fatal("expected error when both checksum and checksums are set")
		}
	})

	t.Run("neither checksum nor checksums means unverified, not an error", func(t *testing.T) {
		got, err := resolveChecksum(QEMUImageSource{}, "arm64")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty string (unverified)", got)
		}
	})
}

func TestParseChecksum(t *testing.T) {
	algo, digest, err := parseChecksum("sha256:ABCDEF")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if algo != "sha256" || digest != "abcdef" {
		t.Errorf("got algo=%q digest=%q", algo, digest)
	}

	if _, _, err := parseChecksum("md5:abcdef"); err == nil {
		t.Error("expected error for unsupported algorithm")
	}

	if _, _, err := parseChecksum("nocolonhere"); err == nil {
		t.Error("expected error for malformed checksum")
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(path, []byte("hello world"), 0600); err != nil {
		t.Fatal(err)
	}

	// sha256("hello world")
	const wantHex = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

	if err := verifyChecksum(path, wantHex); err != nil {
		t.Errorf("expected checksum match, got error: %v", err)
	}

	if err := verifyChecksum(path, "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Error("expected checksum mismatch to error")
	}

	if _, err := os.Stat(path); err != nil {
		t.Errorf("verifyChecksum must never delete the file it checks: %v", err)
	}
}

// TestAcquireBaseImageWithoutChecksumIsUnverifiedNotAnError locks in the
// maintainer's explicit choice to make checksum verification optional: a
// "file" source with no checksum set must still succeed (not error), since
// resolveChecksum treats an unset checksum as "boot unverified", never as a
// validation failure.
func TestAcquireBaseImageWithoutChecksumIsUnverifiedNotAnError(t *testing.T) {
	requireQEMUImg(t)

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "base.qcow2")
	os.WriteFile(imgPath, []byte("not a real image"), 0600)

	path, _, err := acquireBaseImage(QEMUImageSource{Type: "file", Source: "base.qcow2"}, dir, dir, "x86_64")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != imgPath {
		t.Errorf("got path %q, want %q", path, imgPath)
	}
}

func TestAcquireBaseImageChecksumMismatchStillFails(t *testing.T) {
	requireQEMUImg(t)

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "base.qcow2")
	os.WriteFile(imgPath, []byte("not a real image"), 0600)

	_, _, err := acquireBaseImage(QEMUImageSource{Type: "file", Source: "base.qcow2", Checksum: "sha256:0000000000000000000000000000000000000000000000000000000000000000"}, dir, dir, "x86_64")
	if err == nil {
		t.Fatal("expected error when a set checksum doesn't match")
	}
}

func TestFindQcow2InPull(t *testing.T) {
	t.Run("single match with no wantFile", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "only.qcow2"), nil, 0600)

		got, err := findQcow2InPull(dir, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != filepath.Join(dir, "only.qcow2") {
			t.Errorf("got %q, want only.qcow2", got)
		}
	})

	t.Run("ambiguous falls back to image.qcow2 when present", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "variant-a.qcow2"), nil, 0600)
		os.WriteFile(filepath.Join(dir, "image.qcow2"), nil, 0600)
		os.WriteFile(filepath.Join(dir, "variant-b.qcow2"), nil, 0600)

		got, err := findQcow2InPull(dir, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != filepath.Join(dir, "image.qcow2") {
			t.Errorf("got %q, want image.qcow2 fallback", got)
		}
	})

	t.Run("ambiguous with no image.qcow2 errors, never guesses", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "variant-a.qcow2"), nil, 0600)
		os.WriteFile(filepath.Join(dir, "variant-b.qcow2"), nil, 0600)

		_, err := findQcow2InPull(dir, "")
		if err == nil {
			t.Fatal("expected error when ambiguous and no image.qcow2 present")
		}
		if !strings.Contains(err.Error(), "image.file") {
			t.Errorf("expected error to mention image.file, got: %v", err)
		}
	})

	t.Run("explicit wantFile always wins over image.qcow2", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "image.qcow2"), nil, 0600)
		os.WriteFile(filepath.Join(dir, "picked.qcow2"), nil, 0600)

		got, err := findQcow2InPull(dir, "picked.qcow2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != filepath.Join(dir, "picked.qcow2") {
			t.Errorf("got %q, want picked.qcow2", got)
		}
	})
}

func TestPickFreePort(t *testing.T) {
	port, err := pickFreePort()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Errorf("got implausible port %d", port)
	}
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("expected current process to be alive")
	}
	if processAlive(0) {
		t.Error("expected pid 0 to be reported not alive")
	}
}

// requireQEMUImg skips a test if qemu-img isn't on PATH — CI/dev machines
// without qemu installed shouldn't fail the whole suite over these.
func requireQEMUImg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img not found in PATH, skipping")
	}
}

func TestCreateOverlayDisk(t *testing.T) {
	requireQEMUImg(t)

	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.qcow2")

	create := exec.Command("qemu-img", "create", "-f", "qcow2", basePath, "16M")
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("failed to create test base image: %v\n%s", err, out)
	}

	overlayPath, err := createOverlayDisk(basePath, dir, 0)
	if err != nil {
		t.Fatalf("createOverlayDisk failed: %v", err)
	}

	if _, err := os.Stat(overlayPath); err != nil {
		t.Errorf("expected overlay disk to exist: %v", err)
	}
}

func TestGenerateEphemeralSSHKeyAndCloudInitSeed(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not found in PATH, skipping")
	}

	dir := t.TempDir()

	privKeyPath, pubKey, err := generateEphemeralSSHKey(dir)
	if err != nil {
		t.Fatalf("generateEphemeralSSHKey failed: %v", err)
	}

	info, err := os.Stat(privKeyPath)
	if err != nil {
		t.Fatalf("expected private key to exist: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected private key perms 0600, got %v", info.Mode().Perm())
	}
	if pubKey == "" {
		t.Error("expected non-empty public key")
	}

	if _, _, err := isoBuildCommand(dir, []string{"x", "y"}, filepath.Join(dir, "seed.iso")); err != nil {
		t.Skip("no ISO build tool found in PATH, skipping cloud-init seed test")
	}

	isoPath, err := buildCloudInitSeed(dir, "qemu-basics-01", pubKey, false, "52:54:00:00:00:00", nil)
	if err != nil {
		t.Fatalf("buildCloudInitSeed failed: %v", err)
	}

	if _, err := os.Stat(isoPath); err != nil {
		t.Errorf("expected seed ISO to exist: %v", err)
	}

	// The seed ISO's source directory must contain only user-data/meta-data,
	// never the private key or anything else in stateDir.
	seedSrcDir := filepath.Join(dir, "cidata-src")
	entries, err := os.ReadDir(seedSrcDir)
	if err != nil {
		t.Fatalf("failed to read seed source dir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("expected exactly 2 files (user-data, meta-data) in seed source dir, got %d", len(entries))
	}

	// Read user-data and verify password auth is disabled
	dataBytes, err := os.ReadFile(filepath.Join(seedSrcDir, "user-data"))
	if err != nil {
		t.Fatalf("failed to read user-data: %v", err)
	}
	dataStr := string(dataBytes)
	if !strings.Contains(dataStr, "ssh_pwauth: false") {
		t.Errorf("expected ssh_pwauth: false, got: %s", dataStr)
	}
	if !strings.Contains(dataStr, "lock_passwd: true") {
		t.Errorf("expected lock_passwd: true, got: %s", dataStr)
	}

	// Build with passwordAuth = true
	_, err = buildCloudInitSeed(dir, "qemu-basics-01", pubKey, true, "52:54:00:00:00:00", nil)
	if err != nil {
		t.Fatalf("buildCloudInitSeed with passwordAuth=true failed: %v", err)
	}
	dataBytes, err = os.ReadFile(filepath.Join(seedSrcDir, "user-data"))
	if err != nil {
		t.Fatalf("failed to read user-data: %v", err)
	}
	dataStr = string(dataBytes)
	if !strings.Contains(dataStr, "ssh_pwauth: true") {
		t.Errorf("expected ssh_pwauth: true, got: %s", dataStr)
	}
	if !strings.Contains(dataStr, "lock_passwd: false") {
		t.Errorf("expected lock_passwd: false, got: %s", dataStr)
	}
}

func TestDestroyQEMUVMNoStateIsNoop(t *testing.T) {
	const name = "astrona-test-nonexistent-cluster-xyz"

	// qemuStateDir always MkdirAlls, so clean up the empty dir it leaves
	// behind rather than littering the real user cache dir across test runs.
	t.Cleanup(func() {
		if dir, err := qemuStateDir(name); err == nil {
			os.RemoveAll(dir)
		}
	})

	err := DestroyQEMUVM(name)
	if err != nil {
		t.Errorf("expected no-op (nil error) when no state exists, got: %v", err)
	}
}

// TestCreateQEMUVMRefusesDuplicate guards against the ghost-VM bug: a repeat
// `astrona run` (no `astrona destroy` in between) must refuse rather than
// silently launching a second qemu process on top of one already running for
// the same lab and overwriting handle.json out from under it. Fakes an
// "already running" handle.json (PID = this test process, guaranteed alive)
// so the test never needs a real qemu binary — CreateQEMUVM's guard must
// fire before anything qemu-specific runs.
func TestCreateQEMUVMRefusesDuplicate(t *testing.T) {
	const name = "astrona-test-duplicate-vm-xyz"

	stateDir, err := qemuStateDir(name)
	if err != nil {
		t.Fatalf("qemuStateDir failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(stateDir) })

	handle := &QEMUHandle{
		ClusterName: name,
		PID:         os.Getpid(),
		SSHHost:     "127.0.0.1",
		SSHPort:     2222,
		SSHUser:     qemuSSHUser,
		SSHKeyPath:  filepath.Join(stateDir, "id_ed25519"),
		KnownHosts:  filepath.Join(stateDir, "known_hosts"),
		StateDir:    stateDir,
	}
	if err := writeHandleState(handle); err != nil {
		t.Fatalf("writeHandleState failed: %v", err)
	}

	_, err = CreateQEMUVM(name, name, t.TempDir(), &QEMUConfig{}, nil)
	if err == nil {
		t.Fatal("expected CreateQEMUVM to refuse launching a second VM for an already-running lab, got nil error")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected error to mention 'already running', got: %v", err)
	}
}
