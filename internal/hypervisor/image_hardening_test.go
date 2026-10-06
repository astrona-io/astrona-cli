package hypervisor

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/config"
	"astrona/internal/ui"
)

func TestResolveWithinDir(t *testing.T) {
	root := t.TempDir()
	pullDir := filepath.Join(root, "pull")
	outside := filepath.Join(root, "secret")
	mustMkdir(t, filepath.Join(pullDir, "sub"))
	mustWrite(t, filepath.Join(pullDir, "base.qcow2"))
	mustWrite(t, filepath.Join(pullDir, "sub", "nested.qcow2"))
	mustWrite(t, outside)
	mustSymlink(t, outside, filepath.Join(pullDir, "escape.qcow2"))
	mustSymlink(t, filepath.Join(pullDir, "base.qcow2"), filepath.Join(pullDir, "alias.qcow2"))
	mustSymlink(t, root, filepath.Join(pullDir, "up"))

	cases := []struct {
		name string
		path string
		ok   bool
	}{
		{"file inside", filepath.Join(pullDir, "base.qcow2"), true},
		{"nested inside", filepath.Join(pullDir, "sub", "nested.qcow2"), true},
		{"symlink to inside", filepath.Join(pullDir, "alias.qcow2"), true},
		{"dotdot outside", filepath.Join(pullDir, "..", "secret"), false},
		{"absolute outside", outside, false},
		{"symlink escape", filepath.Join(pullDir, "escape.qcow2"), false},
		{"symlinked dir escape", filepath.Join(pullDir, "up", "secret"), false},
		{"dir itself", pullDir, false},
		{"missing", filepath.Join(pullDir, "nope.qcow2"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveWithinDir(tc.path, pullDir)
			if tc.ok && err != nil {
				t.Errorf("expected %s to be accepted, got: %v", tc.path, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("expected %s to be rejected", tc.path)
			}
		})
	}
}

func TestCheckBackingChain(t *testing.T) {
	root := t.TempDir()
	pullDir := filepath.Join(root, "pull")
	mustMkdir(t, pullDir)
	image := filepath.Join(pullDir, "image.qcow2")
	base := filepath.Join(pullDir, "base.qcow2")
	outside := filepath.Join(root, "outside.qcow2")
	mustWrite(t, image)
	mustWrite(t, base)
	mustWrite(t, outside)

	entry := func(filename, format, backing, backingFormat, dataFile string) qemuImgInfo {
		var e qemuImgInfo
		e.Filename, e.Format, e.BackingFilename, e.BackingFilenameFormat = filename, format, backing, backingFormat
		e.FormatSpecific.Data.DataFile = dataFile
		return e
	}

	cases := []struct {
		name  string
		chain []qemuImgInfo
		ok    bool
	}{
		{"delta and base inside", []qemuImgInfo{
			entry(image, "qcow2", "base.qcow2", "qcow2", ""),
			entry(base, "qcow2", "", "", ""),
		}, true},
		{"empty chain", nil, false},
		{"second level escapes", []qemuImgInfo{
			entry(image, "qcow2", "base.qcow2", "qcow2", ""),
			entry(base, "qcow2", outside, "qcow2", ""),
			entry(outside, "qcow2", "", "", ""),
		}, false},
		{"relative filename", []qemuImgInfo{
			entry("image.qcow2", "qcow2", "", "", ""),
		}, false},
		{"raw backing", []qemuImgInfo{
			entry(image, "qcow2", "base.qcow2", "raw", ""),
			entry(base, "raw", "", "", ""),
		}, false},
		{"undeclared backing format", []qemuImgInfo{
			entry(image, "qcow2", "base.qcow2", "", ""),
			entry(base, "qcow2", "", "", ""),
		}, false},
		{"external data file", []qemuImgInfo{
			entry(image, "qcow2", "base.qcow2", "qcow2", ""),
			entry(base, "qcow2", "", "", "/etc/passwd"),
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkBackingChain(tc.chain, pullDir)
			if tc.ok && err != nil {
				t.Errorf("expected chain to be accepted, got: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("expected chain to be rejected")
			}
		})
	}
}

// TestFlattenIfDelta runs the real qemu-img against crafted images: a
// legitimate delta+base pair is flattened, while backing files outside the
// pull dir (relative, absolute, via symlink, or two levels down) and
// external data files are refused before `qemu-img convert` reads them.
func TestFlattenIfDelta(t *testing.T) {
	requireQEMUImg(t)

	qemuImg := func(t *testing.T, dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("qemu-img", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("qemu-img %v failed: %v\n%s", args, err, out)
		}
	}

	setup := func(t *testing.T) (root, pullDir, stateDir string) {
		root = t.TempDir()
		pullDir = filepath.Join(root, "state", "oci-image")
		stateDir = filepath.Join(root, "state")
		mustMkdir(t, pullDir)
		return root, pullDir, stateDir
	}

	t.Run("delta with sibling base is flattened", func(t *testing.T) {
		_, pullDir, stateDir := setup(t)
		qemuImg(t, pullDir, "create", "-q", "-f", "qcow2", "base.qcow2", "16M")
		qemuImg(t, pullDir, "create", "-q", "-f", "qcow2", "-b", "base.qcow2", "-F", "qcow2", "image.qcow2")

		got, err := flattenIfDelta(filepath.Join(pullDir, "image.qcow2"), pullDir, stateDir, io.Discard)
		if err != nil {
			t.Fatalf("flattenIfDelta failed: %v", err)
		}
		if err := rejectEmbeddedBackingFile(got); err != nil {
			t.Errorf("flattened image still has a backing file: %v", err)
		}
	})

	t.Run("non-delta is returned unchanged", func(t *testing.T) {
		_, pullDir, stateDir := setup(t)
		qemuImg(t, pullDir, "create", "-q", "-f", "qcow2", "image.qcow2", "16M")
		path := filepath.Join(pullDir, "image.qcow2")
		got, err := flattenIfDelta(path, pullDir, stateDir, io.Discard)
		if err != nil || got != path {
			t.Errorf("expected unchanged path, got %q, %v", got, err)
		}
	})

	rejects := []struct {
		name  string
		build func(t *testing.T, root, pullDir string)
	}{
		{"relative backing outside pull dir", func(t *testing.T, root, pullDir string) {
			mustWriteContent(t, filepath.Join(root, "secret"), "host secret")
			qemuImg(t, pullDir, "create", "-q", "-f", "qcow2", "-b", "../../secret", "-F", "raw", "image.qcow2", "1M")
		}},
		{"absolute backing outside pull dir", func(t *testing.T, root, pullDir string) {
			secret := filepath.Join(root, "secret")
			mustWriteContent(t, secret, "host secret")
			qemuImg(t, pullDir, "create", "-q", "-f", "qcow2", "-b", secret, "-F", "raw", "image.qcow2", "1M")
		}},
		{"backing symlink escapes pull dir", func(t *testing.T, root, pullDir string) {
			qemuImg(t, root, "create", "-q", "-f", "qcow2", "outside.qcow2", "16M")
			mustSymlink(t, filepath.Join(root, "outside.qcow2"), filepath.Join(pullDir, "base.qcow2"))
			qemuImg(t, pullDir, "create", "-q", "-f", "qcow2", "-b", "base.qcow2", "-F", "qcow2", "image.qcow2")
		}},
		{"second-level backing escapes pull dir", func(t *testing.T, root, pullDir string) {
			qemuImg(t, root, "create", "-q", "-f", "qcow2", "outside.qcow2", "16M")
			qemuImg(t, pullDir, "create", "-q", "-f", "qcow2", "-b", filepath.Join(root, "outside.qcow2"), "-F", "qcow2", "base.qcow2")
			qemuImg(t, pullDir, "create", "-q", "-f", "qcow2", "-b", "base.qcow2", "-F", "qcow2", "image.qcow2")
		}},
		{"external data file", func(t *testing.T, root, pullDir string) {
			qemuImg(t, pullDir, "create", "-q", "-f", "qcow2", "-o", "data_file="+filepath.Join(root, "data.raw"), "image.qcow2", "16M")
		}},
	}
	for _, tc := range rejects {
		t.Run(tc.name, func(t *testing.T) {
			root, pullDir, stateDir := setup(t)
			tc.build(t, root, pullDir)
			if _, err := flattenIfDelta(filepath.Join(pullDir, "image.qcow2"), pullDir, stateDir, io.Discard); err == nil {
				t.Error("expected flattenIfDelta to refuse the image")
			}
		})
	}
}

func TestRejectEmbeddedBackingFileDataFile(t *testing.T) {
	requireQEMUImg(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "image.qcow2")
	cmd := exec.Command("qemu-img", "create", "-q", "-f", "qcow2", "-o", "data_file="+filepath.Join(dir, "data.raw"), path, "16M")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("qemu-img create failed: %v\n%s", err, out)
	}
	err := rejectEmbeddedBackingFile(path)
	if err == nil || !strings.Contains(err.Error(), "external data file") {
		t.Errorf("expected external data file rejection, got: %v", err)
	}
}

func TestIsQEMUProcessFor(t *testing.T) {
	cases := []struct {
		name    string
		cmdline string
		ok      bool
	}{
		{"our vm", "/opt/homebrew/bin/qemu-system-aarch64 -name astrona-lab -machine virt -daemonize", true},
		{"our vm, name last", "qemu-system-x86_64 -machine q35 -name astrona-lab", true},
		{"other vm", "qemu-system-aarch64 -name astrona-lab2 -machine virt", false},
		{"name prefix only", "qemu-system-aarch64 -name astrona-la", false},
		{"not qemu", "/usr/bin/vim -name astrona-lab", false},
		{"qemu without name", "qemu-system-aarch64 -machine virt", false},
		{"unrelated", "/usr/libexec/some-daemon", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isQEMUProcessFor(tc.cmdline, qemuProcessName("lab")); got != tc.ok {
				t.Errorf("isQEMUProcessFor(%q) = %v, want %v", tc.cmdline, got, tc.ok)
			}
		})
	}
}

// TestDestroyQEMUVMChecksPID points handle.json at a live, unrelated process
// (a `sleep`) and checks DestroyQEMUVM only signals it when the command-line
// lookup says it's this VM's qemu — state is removed either way.
func TestDestroyQEMUVMChecksPID(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep not found in PATH, skipping")
	}

	cases := []struct {
		name       string
		cmdline    string
		wantKilled bool
	}{
		{"matching qemu process is stopped", "qemu-system-aarch64 -name astrona-pidcheck -machine virt", true},
		{"reused pid is left alone", "/usr/bin/sleep 30", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())

			sleeper := exec.Command("sleep", "30")
			if err := sleeper.Start(); err != nil {
				t.Fatalf("failed to start sleep: %v", err)
			}
			exited := make(chan struct{})
			go func() { _ = sleeper.Wait(); close(exited) }()
			t.Cleanup(func() { _ = sleeper.Process.Kill(); <-exited })

			orig := processCommandLine
			processCommandLine = func(pid int) (string, error) {
				if pid != sleeper.Process.Pid {
					return "", errors.New("unexpected pid")
				}
				return tc.cmdline, nil
			}
			t.Cleanup(func() { processCommandLine = orig })

			stateDir, err := qemuStateDir("pidcheck")
			if err != nil {
				t.Fatalf("qemuStateDir failed: %v", err)
			}
			data, _ := json.Marshal(&config.QEMUHandle{ClusterName: "pidcheck", PID: sleeper.Process.Pid})
			mustWriteContent(t, filepath.Join(stateDir, "handle.json"), string(data))

			if err := DestroyQEMUVM("pidcheck", ui.Discard()); err != nil {
				t.Fatalf("DestroyQEMUVM failed: %v", err)
			}

			select {
			case <-exited:
				if !tc.wantKilled {
					t.Error("unrelated process was signalled")
				}
			case <-time.After(2 * time.Second):
				if tc.wantKilled {
					t.Error("qemu process was not stopped")
				}
			}
			if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
				t.Errorf("expected state dir to be removed, stat err: %v", err)
			}
		})
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	mustWriteContent(t, path, "x")
}

func mustWriteContent(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}
