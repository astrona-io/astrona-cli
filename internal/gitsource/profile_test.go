package gitsource

import (
	"os"
	"path/filepath"
	"testing"

	"astrona/internal/apphome"
)

func TestDevelopmentGitCache(t *testing.T) {
	apphome.Configure("developer")
	t.Cleanup(func() { apphome.Configure("v1.0.0") })
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := gitCacheDir("https://example.com/labs.git", "main")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got) != filepath.Join(home, ".astrona-dev", "cache", "repos") {
		t.Fatalf("wrong cache: %s", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".astrona")); !os.IsNotExist(err) {
		t.Fatalf("release data touched: %v", err)
	}
}
