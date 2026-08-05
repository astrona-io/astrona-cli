package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// QEMUImageSource describes the VM base image: a local qcow2 file or a URL
// to download one. Checksum is mandatory (see acquireBaseImage) — a VM
// image is a full bootable OS, a much higher blast radius than a script or
// manifest, so unlike ResourceItem there is no unverified path.
type QEMUImageSource struct {
	Type     string `yaml:"type"`     // "file" or "url"
	Source   string `yaml:"source"`
	Checksum string `yaml:"checksum"` // required, "sha256:<hex>"
}

// QEMUConfig is the qemu-specific block of a lab's runtime config.
type QEMUConfig struct {
	Image      QEMUImageSource `yaml:"image"`
	Arch       string          `yaml:"arch"`       // "" default x86_64 | "aarch64" (needs UEFI firmware installed, see locateAArch64Firmware)
	CPUs       int             `yaml:"cpus"`       // 0 => default 2
	MemoryMB   int             `yaml:"memoryMB"`   // 0 => default 2048
	DiskSizeGB int             `yaml:"diskSizeGB"` // 0 => use base image's own size
	SSHPort    int             `yaml:"sshPort"`    // 0 => auto-pick a free host port
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
}

const qemuSSHUser = "astrona"

// maxQEMUImageDownloadBytes bounds a downloaded VM base image. Generous
// compared to maxScriptDownloadBytes since real cloud images run several
// GB, but still bounded rather than unlimited — checksum verification
// happens after the download completes, so this caps how much an
// untrusted URL can make the CLI write to disk before that check runs.
const maxQEMUImageDownloadBytes = 20 * 1024 * 1024 * 1024

// qemuStateDir returns (creating if needed) the per-lab directory that holds
// everything about one qemu VM: overlay disk, cloud-init seed, ephemeral SSH
// key, known_hosts, pidfile, and the persisted QEMUHandle. 0700 because it
// holds an SSH private key.
func qemuStateDir(clusterName string) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user cache dir: %w", err)
	}

	dir := filepath.Join(cacheDir, "astrona", "qemu", clusterName)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("failed to create qemu state dir '%s': %w", dir, err)
	}

	return dir, nil
}

// normalizeArch maps the handful of spellings a lab author might write to
// the arch name qemu-system-<arch> and Go's runtime.GOARCH both use.
func normalizeArch(arch string) string {
	switch strings.ToLower(arch) {
	case "", "x86_64", "amd64":
		return "x86_64"
	case "aarch64", "arm64":
		return "aarch64"
	default:
		return strings.ToLower(arch)
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
// same downloadToTemp helper scripts.go uses) and mandatorily verifies its
// checksum before returning. The returned cleanup only ever removes a
// downloaded temp file — a user's own local base image is never deleted,
// even on a checksum mismatch.
func acquireBaseImage(img QEMUImageSource, baseDir, stateDir string) (string, func(), error) {
	noopCleanup := func() {}

	if strings.TrimSpace(img.Checksum) == "" {
		return "", noopCleanup, fmt.Errorf("qemu image '%s' has no checksum: a checksum ('sha256:<hex>') is required for every VM base image", img.Source)
	}

	_, expectedHex, err := parseChecksum(img.Checksum)
	if err != nil {
		return "", noopCleanup, err
	}

	var path string
	cleanup := noopCleanup

	switch strings.ToLower(img.Type) {
	case "file":
		resolved, err := joinWithinBaseDir(baseDir, img.Source)
		if err != nil {
			return "", noopCleanup, fmt.Errorf("failed to resolve qemu base image path: %w", err)
		}
		path = resolved
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return "", noopCleanup, fmt.Errorf("qemu base image does not exist: %s", path)
		}
	case "url":
		tmpPath, clean, err := downloadToTemp(img.Source, "astrona-qemu-image-*.img", maxQEMUImageDownloadBytes)
		if err != nil {
			return "", noopCleanup, fmt.Errorf("failed to download qemu base image from %s: %w", img.Source, err)
		}
		path = tmpPath
		cleanup = clean
	default:
		return "", noopCleanup, fmt.Errorf("unsupported type '%s' for qemu image (must be 'file' or 'url')", img.Type)
	}

	if err := verifyChecksum(path, expectedHex); err != nil {
		cleanup()
		return "", noopCleanup, err
	}

	_ = stateDir // reserved: image caching by checksum is a possible future optimization, not needed yet

	return path, cleanup, nil
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
		resizeCmd := exec.Command(qemuImgPath, "resize", overlayPath, fmt.Sprintf("%dG", diskSizeGB))
		resizeCmd.Stdout = os.Stdout
		resizeCmd.Stderr = os.Stderr
		if err := resizeCmd.Run(); err != nil {
			return "", fmt.Errorf("failed to resize overlay disk to %dG: %w", diskSizeGB, err)
		}
	}

	return overlayPath, nil
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

// buildCloudInitSeed writes a NoCloud user-data/meta-data pair — creating a
// passwordless-auth-disabled SSH user with pubKey as its only credential —
// into a dedicated subdirectory (never the whole stateDir, which also holds
// the private key and disk images) and packs just those two files into a
// "cidata"-labeled ISO9660 image via whichever ISO tool is available.
func buildCloudInitSeed(stateDir, pubKey string) (string, error) {
	seedSrcDir := filepath.Join(stateDir, "cidata-src")
	if err := os.MkdirAll(seedSrcDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create cloud-init seed source dir: %w", err)
	}

	userData := fmt.Sprintf(`#cloud-config
hostname: astrona-lab
users:
  - name: %s
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: true
    ssh_authorized_keys:
      - %s
ssh_pwauth: false
disable_root: true
`, qemuSSHUser, pubKey)
	metaData := "instance-id: astrona-lab\nlocal-hostname: astrona-lab\n"

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

// CreateQEMUVM boots a new VM for clusterName: acquires and checksum-verifies
// the base image, creates a disposable overlay disk, generates an ephemeral
// SSH key, builds a cloud-init seed, launches qemu-system-* detached in the
// background, and waits for it to become SSH-ready.
func CreateQEMUVM(clusterName, baseDir string, cfg *QEMUConfig) (*QEMUHandle, error) {
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

	basePath, baseCleanup, err := acquireBaseImage(cfg.Image, baseDir, stateDir)
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

	privKeyPath, pubKey, err := generateEphemeralSSHKey(stateDir)
	if err != nil {
		return nil, err
	}

	seedPath, err := buildCloudInitSeed(stateDir, pubKey)
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

		firmwareCode, err := locateAArch64Firmware()
		if err != nil {
			return nil, err
		}

		varsPath, err := createUEFIVars(firmwareCode, stateDir)
		if err != nil {
			return nil, err
		}

		firmwareArgs = []string{
			"-drive", "if=pflash,format=raw,readonly=on,file=" + firmwareCode,
			"-drive", "if=pflash,format=raw,file=" + varsPath,
		}
	}

	args := []string{
		"-name", clusterName,
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
		"-netdev", fmt.Sprintf("user,id=net0,hostfwd=tcp:127.0.0.1:%d-:22", sshPort),
		"-device", "virtio-net-pci,netdev=net0",
		"-display", "none",
		"-serial", "file:"+consolePath,
		"-daemonize",
		"-pidfile", pidfilePath,
	)

	fmt.Printf("Launching qemu VM '%s' (arch=%s accel=%s ssh-port=%d)...\n", clusterName, arch, accel, sshPort)

	cmd := exec.Command(qemuPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to launch qemu VM: %w", err)
	}

	pid, err := readPidfile(pidfilePath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("qemu started but pidfile was not written: %w", err)
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
