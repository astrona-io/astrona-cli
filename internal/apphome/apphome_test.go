package apphome

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfiles(t *testing.T) {
	t.Cleanup(func() { Configure("v1.0.0") })
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "system-cache"))
	for _, tc := range []struct {
		version, name, prefix string
		dev                   bool
	}{
		{"developer", ".astrona-dev", "astrodev-", true},
		{"dev", ".astrona-dev", "astrodev-", true},
		{"", ".astrona-dev", "astrodev-", true},
		{"v0.3.5", ".astrona", "astro-", false},
		{"v0.4.0-rc1", ".astrona", "astro-", false},
	} {
		t.Run(tc.version, func(t *testing.T) {
			Configure(tc.version)
			if Development() != tc.dev || Name() != tc.name || ClusterPrefix() != tc.prefix || TestClusterPrefix() != tc.prefix+"test-" {
				t.Fatalf("wrong profile: %v %s %s", Development(), Name(), ClusterPrefix())
			}
			want := filepath.Join(home, tc.name, "cache", "repos")
			if !tc.dev {
				cache, err := os.UserCacheDir()
				if err != nil {
					t.Fatal(err)
				}
				want = filepath.Join(cache, "astrona", "repos")
			}
			got, err := ReposDir()
			if err != nil || got != want {
				t.Fatalf("ReposDir = %q, %v; want %q", got, err, want)
			}
			if _, err := os.Stat(got); !os.IsNotExist(err) {
				t.Fatalf("path lookup created data: %v", err)
			}
		})
	}
	Configure("developer")
	t.Setenv("HOME", "")
	if _, err := ReposDir(); err == nil {
		t.Fatal("expected missing home error")
	}
}
