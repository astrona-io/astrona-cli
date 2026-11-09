package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Only "no lab here" is silent; a lab that is there but won't load is an
// error even when nothing was named.
func TestLoadLabForCheck(t *testing.T) {
	empty := t.TempDir()
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "config.yaml"), []byte("metadata: [not, a, map\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name     string
		dir      string
		flags    rootFlags
		explicit bool
		wantErr  bool
	}{
		{"no lab here", empty, rootFlags{configPath: ".", fileName: "config.yaml"}, false, false},
		{"malformed config here", bad, rootFlags{configPath: ".", fileName: "config.yaml"}, false, true},
		{"astrona use lab gone", empty, rootFlags{configPath: filepath.Join(empty, "gone"), fileName: "config.yaml", fromCurrent: true}, false, true},
		{"explicit -c missing", empty, rootFlags{configPath: filepath.Join(empty, "gone"), fileName: "config.yaml"}, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Chdir(c.dir)
			flags := c.flags
			cfg, _, cleanup, err := loadLabForCheck(&flags, c.explicit)
			cleanup()
			if cfg != nil || (err != nil) != c.wantErr {
				t.Fatalf("cfg=%v err=%v", cfg, err)
			}
		})
	}
}
