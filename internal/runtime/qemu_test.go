package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

func TestQEMURejectsForeignVMBeforeCreateOrLoad(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	dir := savedQEMUVM(t, "astro-parent-db", "astro-parent-db")
	cfg := config.RuntimeConfig{Type: "qemu", QEMU: []config.QEMUVM{{Name: "db"}}}
	for _, run := range []func() error{
		func() error {
			_, err := CreateEnvironment(context.Background(), "astro-parent", t.TempDir(), cfg, ui.Discard())
			return err
		},
		func() error { _, err := LoadEnvironment("astro-parent", cfg); return err },
	} {
		if err := run(); err == nil || !strings.Contains(err.Error(), "belonging to lab") {
			t.Fatalf("expected ownership refusal before runtime access: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "overlay.qcow2")); err != nil {
		t.Fatal(err)
	}
}

func savedQEMUVM(t *testing.T, name, owner string) string {
	t.Helper()
	base, err := hypervisor.QEMUBaseDir()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(config.QEMUHandle{ClusterName: name, LabName: owner})
	if err != nil {
		t.Fatal(err)
	}
	for file, contents := range map[string][]byte{"handle.json": data, "overlay.qcow2": []byte("student work")} {
		if err := os.WriteFile(filepath.Join(dir, file), contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDestroyQEMUChecksEveryOwnerBeforeDeleting(t *testing.T) {
	for _, owner := range []string{"astro-parent", "astro-parent-db", "astro-another", ""} {
		t.Run("owner="+owner, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			first := savedQEMUVM(t, "astro-parent-api", "astro-parent")
			second := savedQEMUVM(t, "astro-parent-db", owner)
			cfg := config.RuntimeConfig{Type: "qemu", QEMU: []config.QEMUVM{{Name: "api"}, {Name: "db"}}}
			err := DestroyEnvironment("astro-parent", cfg, ui.Discard())
			owned := owner == "astro-parent"
			if (err == nil) != owned {
				t.Fatalf("DestroyEnvironment: %v; owned=%v", err, owned)
			}
			for _, dir := range []string{first, second} {
				_, statErr := os.Stat(filepath.Join(dir, "overlay.qcow2"))
				if owned && !os.IsNotExist(statErr) || !owned && statErr != nil {
					t.Fatalf("disk %s: %v; owned=%v", dir, statErr, owned)
				}
			}
		})
	}
}

func TestQEMUReservationPreservesStandaloneAndBusyNames(t *testing.T) {
	for _, reason := range []string{"standalone state", "mutation lock"} {
		t.Run(reason, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			const child = "astro-parent-db"
			dir := savedQEMUVM(t, child, "astro-parent")
			if reason == "standalone state" {
				if err := labstate.Save(child, &labstate.State{StartedAt: "standalone-run"}); err != nil {
					t.Fatal(err)
				}
			} else {
				release, err := labstate.LockMutation(child)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			cfg := config.RuntimeConfig{Type: "qemu", QEMU: []config.QEMUVM{{Name: "db"}}}
			if err := DestroyEnvironment("astro-parent", cfg, ui.Discard()); err == nil {
				t.Fatal("expected reserved VM name to refuse teardown")
			}
			if _, err := os.Stat(filepath.Join(dir, "overlay.qcow2")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestQEMUSingleVMRejectsForeignParent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := savedQEMUVM(t, "astro-parent-db", "astro-parent")
	cfg := config.RuntimeConfig{Type: "qemu"}
	if err := DestroyEnvironment("astro-parent-db", cfg, ui.Discard()); err == nil {
		t.Fatal("config-based single-VM teardown claimed another lab's child")
	}
	if _, err := os.Stat(filepath.Join(dir, "overlay.qcow2")); err != nil {
		t.Fatal(err)
	}
	// Exact-name cleanup remains available when the user selects that VM.
	if err := hypervisor.DestroyQEMUVM("astro-parent-db", ui.Discard()); err != nil {
		t.Fatal(err)
	}
}

func TestQEMUReservationProtectsChildrenAndReleasesLocks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vms := []config.QEMUVM{{Name: "db"}, {Name: "api"}}
	release, err := reserveQEMUVMs("astro-parent", vms)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"astro-parent-db", "astro-parent-api"} {
		unlock, err := labstate.LockMutation(name)
		if err == nil {
			unlock()
			release()
			t.Fatalf("child %s was not reserved", name)
		}
	}
	release()
	for _, name := range []string{"astro-parent-db", "astro-parent-api"} {
		unlock, err := labstate.LockMutation(name)
		if err != nil {
			t.Fatal(err)
		}
		unlock()
	}
}
