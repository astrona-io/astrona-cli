package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizeArch(t *testing.T) {
	cases := map[string]string{
		"":        "x86_64",
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

func TestAcquireBaseImageRequiresChecksum(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "base.qcow2")
	os.WriteFile(imgPath, []byte("not a real image"), 0600)

	_, _, err := acquireBaseImage(QEMUImageSource{Type: "file", Source: "base.qcow2"}, dir, dir)
	if err == nil {
		t.Fatal("expected error when checksum is empty")
	}
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

	if _, _, err := isoBuildCommand(dir, "x", "y", filepath.Join(dir, "seed.iso")); err != nil {
		t.Skip("no ISO build tool found in PATH, skipping cloud-init seed test")
	}

	isoPath, err := buildCloudInitSeed(dir, pubKey)
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
