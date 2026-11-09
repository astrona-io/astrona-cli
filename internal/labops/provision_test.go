package labops

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"astrona/internal/config"
)

func TestOtherQEMULabs(t *testing.T) {
	vm := func(name, lab string) LabRow { return LabRow{Name: name, Lab: lab} }
	for _, c := range []struct {
		name string
		rows []LabRow
		want []string
	}{
		{"multi-VM lab is one lab",
			[]LabRow{vm("astro-net-vm1", "astro-net"), vm("astro-net-vm2", "astro-net"), vm("astro-db", "astro-db")},
			[]string{"astro-net", "astro-db"}},
		{"own VMs are skipped",
			[]LabRow{vm("astro-k8s-basics-vm1", "astro-k8s-basics"), vm("astro-k8s-basics", "astro-k8s-basics")},
			nil},
		{"a longer lab name isn't this lab's VM",
			[]LabRow{vm("astro-k8s-basics-01", "astro-k8s-basics-01"), vm("astro-k8s-basics-01-vm1", "astro-k8s-basics-01")},
			[]string{"astro-k8s-basics-01"}},
		{"test copies are skipped",
			[]LabRow{vm("astro-test-x-vm1", "astro-test-x")},
			nil},
		{"old handle without a lab: its own name, unless it may be this lab's",
			[]LabRow{vm("astro-db", ""), vm("astro-k8s-basics-vm1", "")},
			[]string{"astro-db"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := OtherQEMULabs("astro-k8s-basics", c.rows); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestLabExistsQEMU(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mark := func(name string) {
		dir := filepath.Join(home, ".astrona", "qemu", name)
		os.MkdirAll(dir, 0700)
		os.WriteFile(filepath.Join(dir, "handle.json"), []byte("{}"), 0600)
	}

	exists := func(cfg *config.LabConfig, name string) bool {
		t.Helper()
		ok, err := LabExists(cfg, name)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}

	multi := &config.LabConfig{Runtime: config.RuntimeConfig{Type: "qemu", QEMU: []config.QEMUVM{{Name: "a"}, {Name: "b"}}}}
	if exists(multi, "astro-net") {
		t.Fatal("nothing exists yet")
	}
	mark("astro-net-b")
	if !exists(multi, "astro-net") {
		t.Fatal("one VM of a multi-VM lab should count as existing")
	}

	single := &config.LabConfig{Runtime: config.RuntimeConfig{Type: "qemu", QEMU: []config.QEMUVM{{}}}}
	mark("astro-one")
	if !exists(single, "astro-one") {
		t.Fatal("single-VM lab not detected")
	}
}
