package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/account"
	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/hypervisor"
	"astrona/internal/labstate"
	"astrona/internal/localdata"
	"astrona/internal/logs"
	"astrona/internal/portforward"
	"astrona/internal/terms"
	"astrona/internal/version"
)

func TestDevelopmentStatePaths(t *testing.T) {
	apphome.Configure("developer")
	t.Cleanup(func() { apphome.Configure("v1.0.0") })
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := filepath.Join(home, ".astrona-dev")
	for name, path := range map[string]func() (string, error){
		"settings.json":     func() (string, error) { return settingsPath(&rootFlags{}) },
		"current.json":      currentLabPath,
		"update-check.json": updateStatePath,
		"bundles":           bundlesDir,
		"bin":               versionsDir,
		"labs":              labstate.Dir,
		"kind":              cluster.KindStateDir,
		"qemu":              hypervisor.QEMUBaseDir,
		"cache/images":      hypervisor.ImageCacheDir,
		"portforward":       portforward.BaseDir,
		"logs":              logs.Dir,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := path()
			if err != nil || got != filepath.Join(base, name) {
				t.Fatalf("path = %q, %v", got, err)
			}
		})
	}
	credentials, err := account.DefaultStore()
	if err != nil || credentials.Path != filepath.Join(base, "credentials.json") {
		t.Fatalf("credentials: %+v, %v", credentials, err)
	}
	receipt, err := terms.DefaultStore()
	if err != nil || receipt.Dir != base {
		t.Fatalf("terms: %+v, %v", receipt, err)
	}
	data, err := localdata.DefaultStore()
	if err != nil || data.Data != base || data.Repos != filepath.Join(base, "cache", "repos") {
		t.Fatalf("data: %+v, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".astrona")); !os.IsNotExist(err) {
		t.Fatalf("release state was touched: %v", err)
	}

	for _, name := range []string{"lab", "astrodev-lab"} {
		if got := config.NormalizeClusterName(name); got != "astrodev-lab" {
			t.Fatalf("cluster name = %s", got)
		}
		if got := config.NormalizeTestClusterName(name); got != "astrodev-test-lab" {
			t.Fatalf("test name = %s", got)
		}
	}
	rows := groupKindContainers("release|running|astro-lab|control-plane\ndev|running|astrodev-lab|control-plane")
	if len(rows) != 1 || len(rows["astrodev-lab"]) != 1 {
		t.Fatalf("release cluster leaked into dev discovery: %v", rows)
	}
	if config.NormalizeClusterName("astro-lab") == "astro-lab" {
		t.Fatal("explicit release name escaped the dev namespace")
	}
	if _, err := installVersion(version.V{Major: 1}); err == nil || !strings.Contains(err.Error(), "development builds") {
		t.Fatalf("release installation allowed: %v", err)
	}
	upgrade := newUpgradeCmd()
	if err := upgrade.RunE(upgrade, nil); err == nil || !strings.Contains(err.Error(), "development builds") {
		t.Fatalf("self-upgrade allowed: %v", err)
	}

	apphome.Configure("v1.0.0")
	rows = groupKindContainers("release|running|astro-lab|control-plane\ndev|running|astrodev-lab|control-plane")
	if len(rows) != 1 || len(rows["astro-lab"]) != 1 {
		t.Fatalf("dev cluster leaked into release discovery: %v", rows)
	}
}

// Exercise the real entrypoint as well as the path helpers: a missed Configure
// call would pass unit tests but make the local binary use release state.
func TestBuiltBinaryProfiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two CLI binaries")
	}
	home := t.TempDir()
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != "HOME" && key != "XDG_CACHE_HOME" && key != "CI" {
			env = append(env, entry)
		}
	}
	env = append(env, "HOME="+home, "XDG_CACHE_HOME="+filepath.Join(home, "cache"), "CI=true")
	for _, tc := range []struct{ version, dir string }{{"developer", ".astrona-dev"}, {"v0.3.5", ".astrona"}} {
		t.Run(tc.version, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "astrona")
			buildArgs := []string{"build", "-o", binary}
			if tc.version != "developer" {
				buildArgs = append(buildArgs, "-ldflags", "-X main.Version="+tc.version)
			}
			build := exec.CommandContext(t.Context(), "go", append(buildArgs, ".")...)
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v\n%s", err, out)
			}
			run := func(args ...string) string {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), binary, args...)
				cmd.Env = env
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%v: %v\n%s", args, err, out)
				}
				return string(out)
			}
			base := filepath.Join(home, tc.dir)
			if got := strings.TrimSpace(run("settings", "path")); got != filepath.Join(base, "settings.json") {
				t.Fatalf("settings path: %s", got)
			}
			if got := run("settings", "--help"); !strings.Contains(got, "~/"+tc.dir+"/settings.json") {
				t.Fatalf("wrong help: %s", got)
			}
			run("terms", "--accept-terms")
			run("settings", "set", "parallel", "2", "--accept-terms")
			for _, name := range []string{"terms.json", "settings.json"} {
				if _, err := os.Stat(filepath.Join(base, name)); err != nil {
					t.Fatal(err)
				}
			}
			other := ".astrona"
			if other == tc.dir {
				other = ".astrona-dev"
			}
			otherSettings := filepath.Join(home, other, "settings.json")
			if err := os.MkdirAll(filepath.Dir(otherSettings), 0o700); err != nil {
				t.Fatal(err)
			}
			const sentinel = `{"parallel":4}`
			if err := os.WriteFile(otherSettings, []byte(sentinel), 0o600); err != nil {
				t.Fatal(err)
			}
			run("data", "reset", "--yes")
			if _, err := os.Stat(filepath.Join(base, "settings.json")); !os.IsNotExist(err) {
				t.Fatalf("reset did not remove own settings: %v", err)
			}
			if got, err := os.ReadFile(otherSettings); err != nil || string(got) != sentinel {
				t.Fatalf("reset changed other profile: %s, %v", got, err)
			}
			if tc.version == "developer" {
				devCache := filepath.Join(base, "cache", "repos", "fixture", "entry")
				releaseCache := filepath.Join(home, ".astrona", "cache", "images", "keep.img")
				for _, path := range []string{devCache, releaseCache} {
					if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("keep release data"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				run("data", "clean", "--yes")
				if _, err := os.Stat(devCache); !os.IsNotExist(err) {
					t.Fatalf("dev cache not removed: %v", err)
				}
				if got, err := os.ReadFile(releaseCache); err != nil || string(got) != "keep release data" {
					t.Fatalf("release cache changed: %s, %v", got, err)
				}
			}
		})
	}
}

func TestDevelopmentCacheUsageCountsReposOnce(t *testing.T) {
	base := t.TempDir()
	repos := filepath.Join(base, "cache", "repos")
	if err := os.MkdirAll(repos, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repos, "entry"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := localdata.Store{Data: base, Repos: repos}
	usage, err := store.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var bytes int64
	for _, item := range usage {
		bytes += item.Bytes
	}
	if bytes != 5 {
		t.Fatalf("cache counted more than once: %d bytes", bytes)
	}
}
