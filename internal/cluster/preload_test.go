package cluster

import (
	"io"
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
  "save --help") echo "${ASTRONA_TEST_SAVE_HELP:-save -o}" ;;
  "version --format") echo "${ASTRONA_TEST_SERVER_PLATFORM:-1.48 linux/amd64}" ;;
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

func TestPodmanSaveQualifiesDockerHubImages(t *testing.T) {
	for _, tc := range []struct{ image, want string }{
		{"nginx:1.27-alpine", "docker.io/library/nginx:1.27-alpine"},
		{"team/app:1", "docker.io/team/app:1"},
		{"nginx@sha256:abc", "docker.io/library/nginx@sha256:abc"},
		{"docker.io/library/nginx:1.27-alpine", "docker.io/library/nginx:1.27-alpine"},
		{"quay.io/team/app:1", "quay.io/team/app:1"},
		{"localhost:5000/app:1", "localhost:5000/app:1"},
		{"localhost/app:1", "localhost/app:1"},
	} {
		t.Run(tc.image, func(t *testing.T) {
			dir := t.TempDir()
			calls := filepath.Join(dir, "calls")
			path := filepath.Join(dir, "podman")
			body := "#!/bin/sh\necho \"$*\" >> \"" + calls + "\"\n[ \"$1\" != image ]\n"
			if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := saveImage(ContainerEngine{Name: "podman", Path: path}, tc.image, "archive.tar", io.Discard); err != nil {
				t.Fatal(err)
			}
			log := readFile(t, calls) + "\n"
			for _, want := range []string{"image inspect --format {{.Id}} " + tc.want, "pull " + tc.want, "save -o archive.tar " + tc.want} {
				if !strings.Contains(log, want+"\n") {
					t.Errorf("missing %q in calls:\n%s", want, log)
				}
			}
		})
	}
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

func TestDockerSavePlatform(t *testing.T) {
	for _, tc := range []struct {
		name, server, want string
	}{
		{"amd64", "1.48 linux/amd64", "--platform linux/amd64"},
		{"arm64", "1.52 linux/arm64", "--platform linux/arm64"},
		{"older daemon", "1.47 linux/amd64", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := fakeEngineAndKind(t)
			t.Setenv("ASTRONA_TEST_SAVE_HELP", "save --platform API 1.48+")
			t.Setenv("ASTRONA_TEST_SERVER_PLATFORM", tc.server)
			if err := PreloadImages("astro-x", []string{"nginx:1.27-alpine"}, ui.Discard()); err != nil {
				t.Fatal(err)
			}
			log := readFile(t, calls)
			for _, line := range strings.Split(log, "\n") {
				if !strings.HasPrefix(line, "docker save -o ") {
					continue
				}
				if tc.want == "" && strings.Contains(line, "--platform") || tc.want != "" && !strings.Contains(line, tc.want) {
					t.Errorf("save call = %q, want platform %q", line, tc.want)
				}
			}
		})
	}
}
