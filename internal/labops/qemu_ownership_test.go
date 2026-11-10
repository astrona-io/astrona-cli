package labops

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

func writeOwnedQEMU(t *testing.T, name, owner string) string {
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
	if err := os.WriteFile(filepath.Join(dir, "handle.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDiscoveredQEMUChildRechecksOwnership(t *testing.T) {
	for _, changed := range []string{"owner", "standalone state", "mutation lock", "unchanged"} {
		t.Run(changed, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			const parent, child = "astro-parent", "astro-parent-db"
			dir := writeOwnedQEMU(t, child, parent)
			names := hypervisor.MultiVMs(parent)
			if len(names) != 1 || names[0] != child {
				t.Fatalf("discovery: %v", names)
			}
			switch changed {
			case "owner":
				writeOwnedQEMU(t, child, child)
			case "standalone state":
				if err := labstate.Save(child, &labstate.State{StartedAt: "new-run"}); err != nil {
					t.Fatal(err)
				}
			case "mutation lock":
				release, err := labstate.LockMutation(child)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			}
			err := destroyOwnedQEMUVM(parent, names[0], ui.Discard())
			if (err == nil) != (changed == "unchanged") {
				t.Fatalf("destroy after %s: %v", changed, err)
			}
			_, statErr := os.Stat(dir)
			if changed == "unchanged" && !os.IsNotExist(statErr) || changed != "unchanged" && statErr != nil {
				t.Fatalf("state after %s: %v", changed, statErr)
			}
		})
	}
}

func TestExactQEMUNameCanRemoveChild(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	dir := writeOwnedQEMU(t, "astro-parent-db", "astro-parent")
	if err := DestroyByName("astro-parent-db", ui.Discard(), DestroyOptions{Out: io.Discard, ForgetWarn: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("explicitly selected VM was kept: %v", err)
	}
}
