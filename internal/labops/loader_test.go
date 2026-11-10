package labops

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/gitsource"
)

func TestGitConfigContainmentFreshAndSaved(t *testing.T) {
	for _, target := range []string{"outside", "dangling", "tracked"} {
		t.Run(target, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			repo := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
				cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
			}
			git("init", "-q", "--initial-branch=main")
			path := filepath.Join(t.TempDir(), "outside.yaml")
			if target == "tracked" {
				path = filepath.Join(repo, "tracked.yaml")
			}
			if target != "dangling" {
				if err := os.WriteFile(path, []byte("metadata: {name: demo}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			link := path
			if target == "tracked" {
				link = "tracked.yaml"
			}
			if err := os.Symlink(link, filepath.Join(repo, "config.yaml")); err != nil {
				t.Fatal(err)
			}
			git("add", ".")
			git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
			src := LabSource{Git: repo, Config: ".", File: "config.yaml"}
			// Saved loads must reject an unsafe cache even if an older CLI
			// already cloned it and remembered a running lab.
			if _, err := gitsource.ResolveGitConfigSource(repo, "", false); err != nil {
				t.Fatal(err)
			}
			for _, load := range []func() (*LoadedLab, error){
				func() (*LoadedLab, error) { return LoadLab(src, LoadOptions{Log: io.Discard}) },
				func() (*LoadedLab, error) { return LoadSavedLab("astro-demo", src, LoadOptions{}) },
			} {
				loaded, err := load()
				if target != "tracked" {
					if err == nil || !strings.Contains(err.Error(), "cloned repo") {
						t.Fatalf("unsafe config link accepted: %v", err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if loaded.Config.Metadata.Name != "demo" {
					t.Fatalf("wrong config loaded: %s", loaded.Config.Metadata.Name)
				}
				loaded.Cleanup()
			}
		})
	}
}
