package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/ui"
)

// fakeEngineAndKind installs a fake `docker` (which DetectContainerEngine
// prefers) and `kind` that log every call. The engine "has" only images
// named "cached:*".
func fakeEngineAndKind(t *testing.T) (calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	docker := `#!/bin/sh
echo "docker $*" >> "` + calls + `"
case "$1 $2" in
  "image inspect") case "$5" in cached:*) exit 0 ;; *) exit 1 ;; esac ;;
  "pull "*) exit 0 ;;
  "save -o") echo archive > "$3" ;;
esac
`
	kind := `#!/bin/sh
echo "kind $*" >> "` + calls + `"
[ -s "$3" ] || { echo "archive missing" >&2; exit 1; }
`
	for name, body := range map[string]string{"docker": docker, "kind": kind} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func TestPreloadImages(t *testing.T) {
	calls := fakeEngineAndKind(t)

	if err := PreloadImages("astro-x", []string{"cached:1", "fresh:2"}, ui.Discard()); err != nil {
		t.Fatalf("PreloadImages: %v", err)
	}

	log := readFile(t, calls)
	if strings.Contains(log, "docker pull cached:1") {
		t.Error("pulled an image the engine already had")
	}
	if !strings.Contains(log, "docker pull fresh:2") {
		t.Error("did not pull a missing image")
	}

	var loads []string
	for _, line := range strings.Split(log, "\n") {
		if strings.HasPrefix(line, "kind load image-archive ") {
			loads = append(loads, line)
			if !strings.HasSuffix(line, "--name astro-x") {
				t.Errorf("kind load not pinned to the cluster: %q", line)
			}
			archive := strings.Fields(line)[3]
			if _, err := os.Stat(archive); !os.IsNotExist(err) {
				t.Errorf("temp archive %s not removed", archive)
			}
		}
	}
	if len(loads) != 2 {
		t.Fatalf("kind load calls = %d, want 2:\n%s", len(loads), log)
	}
}

func TestPreloadImagesNothingToDo(t *testing.T) {
	if err := PreloadImages("astro-x", nil, ui.Discard()); err != nil {
		t.Fatal(err)
	}
}
