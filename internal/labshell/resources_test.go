package labshell

import (
	"os"
	"path/filepath"
	"testing"

	"astrona/internal/config"
	"astrona/internal/resources"
)

func TestWorkspaceResourcesKeepEditsAndReset(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "resources"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "resources/count-versions.sh"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := resources.Collect(&config.LabConfig{}, source)
	if err != nil {
		t.Fatal(err)
	}
	for _, lab := range []string{"astro-one", "astro-two"} {
		if _, err := resources.Snapshot(lab, source, list); err != nil {
			t.Fatal(err)
		}
		root, path, err := prepareWorkspace(lab)
		if err != nil {
			t.Fatal(err)
		}
		if err := prepareResources(root, path, lab); err != nil {
			t.Fatal(err)
		}
		root.Close()
	}
	dir, err := ResourceDir("astro-one")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "count-versions.sh"), []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, path, err := prepareWorkspace("astro-one")
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareResources(root, path, "astro-one"); err != nil {
		t.Fatal(err)
	}
	root.Close()
	if data, _ := os.ReadFile(filepath.Join(dir, "count-versions.sh")); string(data) != "edited" {
		t.Fatal("reopening overwrote edits")
	}
	other, _ := ResourceDir("astro-two")
	if data, _ := os.ReadFile(filepath.Join(other, "count-versions.sh")); string(data) != "original" {
		t.Fatal("labs shared resources")
	}
	if err := Remove(t.Context(), "astro-one"); err != nil {
		t.Fatal(err)
	}
	root, path, err = prepareWorkspace("astro-one")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := prepareResources(root, path, "astro-one"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "count-versions.sh")); string(data) != "original" {
		t.Fatal("new run did not restore original resources")
	}
}
