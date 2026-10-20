package resources

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
)

func TestResourceWorkspaceHelper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	source := t.TempDir()
	files := map[string]string{
		"count-versions.sh": "printf '<%s>\\n' \"$@\"; cat sibling.txt\n",
		"sibling.txt":       "sibling data\n",
		"nested/tool.sh":    "printf 'nested:%s\\n' \"$1\"\n",
	}
	for name, content := range files {
		path := filepath.Join(source, "resources", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	list, err := Collect(&config.LabConfig{Resources: []config.LabResource{{File: "nested/tool.sh"}}}, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Snapshot("astro-test", source, list); err != nil {
		t.Fatal(err)
	}
	bundle, id, cleanup, err := WorkspaceBundle("astro-test")
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if id == "" {
		t.Fatal("missing workspace ID")
	}
	for _, name := range []string{"count-versions", "count-versions.sh", "nested/tool.sh"} {
		if _, ok := Find(list, name); !ok {
			t.Fatalf("filename not recognized: %s", name)
		}
		cmd := exec.Command(filepath.Join(bundle, "bin/astrona"), "res", "run", name, "--", "http://scout/0", "a b", "$(touch injected)", "'quoted'", "--flag")
		cmd.Dir = t.TempDir()
		cmd.Env = append(os.Environ(), "ASTRONA_LAB=astro-test", "ASTRONA_RESOURCE_DIR="+filepath.Join(bundle, "resources"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, out)
		}
		if name != "nested/tool.sh" {
			for _, want := range []string{"<http://scout/0>", "<a b>", "<$(touch injected)>", "<'quoted'>", "<--flag>", "sibling data"} {
				if !strings.Contains(string(out), want) {
					t.Fatalf("argument or sibling file lost: %s", out)
				}
			}
		} else if !strings.Contains(string(out), "nested:http://scout/0") {
			t.Fatal(string(out))
		}
		if _, err := os.Stat(filepath.Join(bundle, "resources", "injected")); !os.IsNotExist(err) {
			t.Fatal("evaluated student arguments")
		}
	}
	// Full reset changes the receipt even when the files are unchanged.
	if _, err := Snapshot("astro-test", source, list); err != nil {
		t.Fatal(err)
	}
	newID, err := WorkspaceID("astro-test")
	if err != nil || newID == id {
		t.Fatalf("reset did not change ID: %v", err)
	}
}

func TestWorkspaceCopyRefusesSnapshotLinks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "resources"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "resources/tool.sh"), []byte("echo ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := Collect(&config.LabConfig{}, source)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := Snapshot("astro-test", source, list)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "tool.sh")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "tool.sh")); err != nil {
		t.Fatal(err)
	}
	if _, _, cleanup, err := WorkspaceBundle("astro-test"); err == nil {
		cleanup()
		t.Fatal("copied a symlink")
	}
}
