package localdata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testStore(t *testing.T) Store {
	t.Helper()
	base := t.TempDir()
	return Store{Data: filepath.Join(base, ".astrona"), Repos: filepath.Join(base, "cache", "astrona", "repos")}
}

func put(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if want && err != nil {
		t.Fatalf("missing %s: %v", path, err)
	}
	if !want && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to be gone; error: %v", path, err)
	}
}

func TestUsageMissingReadOnly(t *testing.T) {
	s := testStore(t)
	u, err := s.Usage(t.Context())
	if err != nil || len(u) != 0 {
		t.Fatalf("usage = %v, %v", u, err)
	}
	if _, err := s.PlanClean(t.Context(), false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlanReset(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertExists(t, s.Data, false)
	assertExists(t, s.Repos, false)
}

func TestUsageCountsWithoutReadingSecretsOrFollowingLinks(t *testing.T) {
	s := testStore(t)
	put(t, filepath.Join(s.Data, "credentials.json"))
	put(t, filepath.Join(s.Data, "resources", "work.yaml"))
	put(t, filepath.Join(s.Repos, "abc", "config.yaml"))
	outside := filepath.Join(t.TempDir(), "outside")
	put(t, outside)
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(s.Data, "external")); err != nil {
		t.Fatal(err)
	}
	u, err := s.Usage(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var files, size, links int64
	for _, entry := range u {
		files += entry.Files
		size += entry.Bytes
		links += entry.Symlinks
	}
	if files != 3 || size != 12 || links != 1 {
		t.Fatalf("unexpected usage: %+v", u)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Usage(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestCleanOnlyDisposableData(t *testing.T) {
	s := testStore(t)
	remove := []string{"cache/images/base.img", "cache/addons/addon.yaml", "catalog.json", "update-check.json"}
	keep := []string{"credentials.json", "trust.json", "settings.json", "current.json", "catalog-sources.json", "results/lab.jsonl", "resources/work.yaml", "bundles/course/config.yaml", "bin/astrona", "logs/log.txt", "diagnostics/report", "unknown/data", "tmp/unmanaged"}
	for _, rel := range append(append([]string{}, remove...), keep...) {
		put(t, filepath.Join(s.Data, rel))
	}
	put(t, filepath.Join(s.Repos, "abc", "config.yaml"))
	outside := filepath.Join(t.TempDir(), "keep")
	put(t, outside)
	if err := os.Symlink(outside, filepath.Join(s.Repos, "abc", "external")); err != nil {
		t.Fatal(err)
	}
	p, err := s.PlanClean(t.Context(), false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 5 {
		t.Fatalf("plan = %+v", p)
	}
	for _, rel := range remove {
		assertExists(t, filepath.Join(s.Data, rel), true)
	}
	if err := p.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p.Removed != 5 || !p.Applied {
		t.Fatalf("result = %+v", p)
	}
	for _, rel := range remove {
		assertExists(t, filepath.Join(s.Data, rel), false)
	}
	for _, rel := range keep {
		assertExists(t, filepath.Join(s.Data, rel), true)
	}
	assertExists(t, outside, true)
}

func TestSavedLabsBlockClean(t *testing.T) {
	for _, state := range []string{"labs", "qemu", "kind", "exams", "portforward"} {
		t.Run(state, func(t *testing.T) {
			s := testStore(t)
			put(t, filepath.Join(s.Data, state, "stopped-lab"))
			put(t, filepath.Join(s.Data, "cache", "images", "base.img"))
			put(t, filepath.Join(s.Repos, "abc", "config.yaml"))
			p, err := s.PlanClean(t.Context(), false, time.Now())
			if err != nil || len(p.Items) != 0 || len(p.Skipped) != 1 {
				t.Fatalf("plan = %+v, %v", p, err)
			}
			if err := p.Apply(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertExists(t, filepath.Join(s.Data, "cache", "images", "base.img"), true)
		})
	}
}

func TestTempOnlyOldKnownPartialFiles(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	for _, rel := range []string{"cache/images/.cache-old.tmp", "cache/addons/.dl-old", "cache/images/.cache-new.tmp", "cache/images/base.img", "cache/addons/manifest.yaml"} {
		path := filepath.Join(s.Data, rel)
		put(t, path)
		if strings.Contains(rel, "old") {
			old := now.Add(-25 * time.Hour)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	p, err := s.PlanClean(t.Context(), true, now)
	if err != nil || len(p.Items) != 2 {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	if err := p.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(s.Data, "cache/images/.cache-new.tmp"), true)
	assertExists(t, filepath.Join(s.Data, "cache/images/base.img"), true)
}

func TestResetMalformedSettingsPreservesUserData(t *testing.T) {
	s := testStore(t)
	for _, name := range []string{"settings.json", "current.json", "update-check.json", "catalog.json", "credentials.json", "trust.json", "terms.json", "catalog-sources.json", "results/lab.jsonl", "labs/active.json"} {
		put(t, filepath.Join(s.Data, name))
	}
	p, err := s.PlanReset(t.Context())
	if err != nil || len(p.Items) != 4 {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	if err := p.Apply(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(s.Data, "settings.json"), false)
	for _, name := range []string{"credentials.json", "trust.json", "terms.json", "catalog-sources.json", "results/lab.jsonl", "labs/active.json"} {
		assertExists(t, filepath.Join(s.Data, name), true)
	}
}

func TestSymlinkedManagedDirectoriesRejected(t *testing.T) {
	for _, rel := range []string{".", "cache", "cache/images", "labs"} {
		t.Run(rel, func(t *testing.T) {
			s := testStore(t)
			target := filepath.Join(s.Data, rel)
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			put(t, filepath.Join(outside, "valuable"))
			if err := os.Symlink(outside, target); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PlanClean(t.Context(), false, time.Now()); err == nil {
				t.Fatal("accepted symlinked cache/state directory")
			}
			assertExists(t, filepath.Join(outside, "valuable"), true)
		})
	}
}

func TestChangedPlanAndSymlinkResetAreRejected(t *testing.T) {
	s := testStore(t)
	put(t, filepath.Join(s.Data, "settings.json"))
	put(t, filepath.Join(s.Data, "current.json"))
	p, err := s.PlanReset(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Data, "current.json"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(t.Context()); err == nil {
		t.Fatal("accepted changed plan")
	}
	assertExists(t, filepath.Join(s.Data, "settings.json"), true)
	if err := os.Rename(filepath.Join(s.Data, "settings.json"), filepath.Join(s.Data, "saved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("saved", filepath.Join(s.Data, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlanReset(t.Context()); err == nil {
		t.Fatal("accepted symlink settings")
	}
}

func TestReplacedCacheParentRejected(t *testing.T) {
	s := testStore(t)
	put(t, filepath.Join(s.Data, "cache/images/base.img"))
	p, err := s.PlanClean(t.Context(), false, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(s.Data, "cache")
	if err := os.Rename(cache, cache+"-old"); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(s.Data, "cache/images/base.img"))
	if err := p.Apply(t.Context()); err == nil {
		t.Fatal("accepted replaced parent")
	}
	assertExists(t, filepath.Join(s.Data, "cache/images/base.img"), true)
}

func TestMaintenanceLocks(t *testing.T) {
	s := testStore(t)
	a, err := s.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer a()
	b, err := s.Lock(false)
	if err != nil {
		t.Fatal(err)
	}
	defer b()
	if release, err := s.Lock(true); err == nil {
		release()
		t.Fatal("exclusive lock acquired during shared use")
	}
	a()
	b()
	exclusive, err := s.Lock(true)
	if err != nil {
		t.Fatal(err)
	}
	defer exclusive()
	if release, err := s.Lock(false); err == nil {
		release()
		t.Fatal("shared lock acquired during maintenance")
	}
	settings, err := s.LockSettings()
	if err != nil {
		t.Fatal(err)
	}
	defer settings()
	if release, err := s.LockSettings(); err == nil {
		release()
		t.Fatal("settings lock not exclusive")
	}
}

func TestDefaultPaths(t *testing.T) {
	s, err := DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if s.Data != filepath.Join(home, ".astrona") || s.Repos != filepath.Join(cache, "astrona", "repos") {
		t.Fatalf("paths = %+v", s)
	}
}

func TestSpecialCacheEntriesKept(t *testing.T) {
	s := testStore(t)
	base := filepath.Join(s.Data, "cache", "addons")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(base, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := s.PlanClean(t.Context(), false, time.Now())
	if err != nil || len(p.Items) != 0 || len(p.Skipped) != 2 {
		t.Fatalf("plan = %+v, %v", p, err)
	}
}

func TestUnsafeLockAndCanceledApply(t *testing.T) {
	s := testStore(t)
	put(t, filepath.Join(s.Data, "settings.json"))
	p, err := s.PlanReset(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := p.Apply(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("apply = %v", err)
	}
	assertExists(t, filepath.Join(s.Data, "settings.json"), true)
	if err := os.Symlink("settings.json", filepath.Join(s.Data, ".maintenance.lock")); err != nil {
		t.Fatal(err)
	}
	if release, err := s.Lock(true); err == nil {
		release()
		t.Fatal("followed a symlink for the maintenance lock")
	}
	if err := syscall.Mkfifo(filepath.Join(s.Data, "settings.json.lock"), 0o600); err != nil {
		t.Fatal(err)
	}
	if release, err := s.LockSettings(); err == nil {
		release()
		t.Fatal("accepted FIFO lock")
	}
	for _, path := range []string{"/", "."} {
		if r, err := openDir(path); err == nil {
			r.Close()
			t.Fatal("accepted broad or relative root")
		}
	}
}

func TestSymlinkedGitCacheParentRejected(t *testing.T) {
	s := testStore(t)
	parent := filepath.Dir(s.Repos)
	if err := os.MkdirAll(filepath.Dir(parent), 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	put(t, filepath.Join(outside, "repos", "precious", "config.yaml"))
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Usage(t.Context()); err == nil {
		t.Fatal("followed symlinked Git cache parent during inventory")
	}
	if _, err := s.PlanClean(t.Context(), false, time.Now()); err == nil {
		t.Fatal("followed symlinked Git cache parent during cleanup")
	}
	assertExists(t, filepath.Join(outside, "repos", "precious", "config.yaml"), true)
}
