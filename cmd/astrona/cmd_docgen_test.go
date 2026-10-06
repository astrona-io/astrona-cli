package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `docgen --man` writes a section-1 man page per public command — what the
// Homebrew formula installs into man1.
func TestDocgenMan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ASTRONA_NO_UPDATE_CHECK", "1")
	out := t.TempDir()

	cmd := newDocgenCmd(&rootFlags{})
	cmd.SetArgs([]string{"--man", "--output", out})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	for _, page := range []string{"astrona.1", "astrona-run.1", "astrona-upgrade.1"} {
		b, err := os.ReadFile(filepath.Join(out, page))
		if err != nil {
			t.Errorf("%s: %v", page, err)
			continue
		}
		if !strings.Contains(string(b), `.TH "ASTRONA" "1"`) {
			t.Errorf("%s isn't a section-1 man page:\n%.200s", page, b)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "astrona-docgen.1")); err == nil {
		t.Error("hidden docgen command got a man page")
	}
}
