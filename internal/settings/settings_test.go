package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsPathDefaultsToAstronaDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Path("")
	if err != nil || got != filepath.Join(home, ".astrona", "settings.json") {
		t.Fatalf("default path: %q, %v", got, err)
	}
}
