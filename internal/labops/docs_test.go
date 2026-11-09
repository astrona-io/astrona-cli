package labops

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadLabDocStaysInsideLab(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0700)
	os.WriteFile(filepath.Join(dir, "docs", "q.md"), []byte("# Q\n"), 0600)
	cfgPath := filepath.Join(dir, "config.yaml")

	if got, err := ReadLabDoc(cfgPath, "docs/q.md"); err != nil || got != "# Q\n" {
		t.Fatalf("ReadLabDoc = %q, %v", got, err)
	}
	if _, err := ReadLabDoc(cfgPath, "../../etc/passwd"); err == nil {
		t.Fatal("doc path escaping the lab dir was read")
	}

	for _, bad := range []string{"../secret.md", "/etc/passwd", "https://evil.example/x.md"} {
		if _, err := ReadLabDoc("https://labs.example/lab/config.yaml", bad); err == nil {
			t.Errorf("remote doc path %q accepted", bad)
		}
	}
}
