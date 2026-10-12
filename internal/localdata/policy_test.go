package localdata

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func policyFile(t *testing.T, path string, size int64, modified time.Time) {
	t.Helper()
	put(t, path)
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func TestCachePolicyAgeAndBudget(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name  string
		days  int
		limit int64
		want  []string
	}{
		{"default age", 30, 1000, []string{"old.yaml"}},
		{"unlimited size still expires", 30, 0, []string{"old.yaml"}},
		{"custom age", 60, 0, []string{}},
		{"oldest first", 60, 20, []string{"middle.yaml", "old.yaml"}},
		{"exact budget", 60, 60, []string{}},
		{"budget smaller than entry", 60, 5, []string{"middle.yaml", "new.yaml", "old.yaml"}},
		{"age boundary", 10, 0, []string{"middle.yaml", "old.yaml"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			for name, days := range map[string]int{"old.yaml": 40, "middle.yaml": 10, "new.yaml": 1} {
				policyFile(t, filepath.Join(s.Data, "cache/addons", name), 20, now.Add(-time.Duration(days)*24*time.Hour))
			}
			p, err := s.PlanPolicy(t.Context(), CachePolicy{time.Duration(tc.days) * 24 * time.Hour, tc.limit}, now)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, item := range p.Items {
				got = append(got, item.name)
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("delete %v, want %v", got, tc.want)
			}
			if err := p.Apply(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCachePolicyCombinedBudgetAndImageSidecars(t *testing.T) {
	for _, suffix := range []string{".qcow2.meta.json", ".meta.json"} {
		t.Run(suffix, func(t *testing.T) {
			s := testStore(t)
			now := time.Now()
			old := now.Add(-10 * 24 * time.Hour)
			image := filepath.Join(s.Data, "cache/images/base.qcow2")
			meta := filepath.Join(s.Data, "cache/images/base"+suffix)
			policyFile(t, image, 60, old)
			policyFile(t, meta, 10, old)
			policyFile(t, filepath.Join(s.Data, "cache/addons/a.yaml"), 20, now)
			policyFile(t, filepath.Join(s.Repos, "repo/config.yaml"), 30, now)
			p, err := s.PlanPolicy(t.Context(), CachePolicy{30 * 24 * time.Hour, 60}, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Items) != 2 || p.Items[0].base != filepath.Dir(image) || p.Items[1].base != filepath.Dir(image) {
				t.Fatalf("plan=%+v", p)
			}
			if err := p.Apply(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertExists(t, image, false)
			assertExists(t, meta, false)
			assertExists(t, filepath.Join(s.Repos, "repo/config.yaml"), true)
		})
	}
}

func TestCachePolicyDirectoryAgeUsesNewestDescendant(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	old := now.Add(-90 * 24 * time.Hour)
	path := filepath.Join(s.Repos, "repo/nested/config.yaml")
	policyFile(t, path, 4, now)
	for _, dir := range []string{filepath.Dir(path), filepath.Join(s.Repos, "repo")} {
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.PlanPolicy(t.Context(), CachePolicy{30 * 24 * time.Hour, 0}, now)
	if err != nil || len(p.Items) != 0 {
		t.Fatalf("recent file discarded: %+v, %v", p, err)
	}
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	p, err = s.PlanPolicy(t.Context(), CachePolicy{30 * 24 * time.Hour, 0}, now)
	if err != nil || len(p.Items) != 1 {
		t.Fatalf("old repo kept: %+v, %v", p, err)
	}
}

func TestCachePolicyProtectsSavedStateAndPersonalFiles(t *testing.T) {
	for _, state := range []string{"labs/lab.json", "qemu/stopped/disk.qcow2", "kind/lab.json", "exams/lab.json", "portforward/lab.json"} {
		t.Run(state, func(t *testing.T) {
			s := testStore(t)
			cache := filepath.Join(s.Data, "cache/images/base.qcow2")
			policyFile(t, cache, 100, time.Now().Add(-90*24*time.Hour))
			put(t, filepath.Join(s.Data, state))
			p, err := s.PlanPolicy(t.Context(), CachePolicy{24 * time.Hour, 1}, time.Now())
			if err != nil || len(p.Items) != 0 || len(p.Skipped) == 0 {
				t.Fatalf("plan=%+v, %v", p, err)
			}
			if err := p.Apply(t.Context()); err != nil {
				t.Fatal(err)
			}
			assertExists(t, cache, true)
		})
	}
	s := testStore(t)
	for _, name := range []string{"credentials.json", "terms.json", "trust.json", "settings.json", "resources/work.txt", "results/grade.json", "bundles/b/config.yaml", "logs/log.txt", "bin/astrona"} {
		put(t, filepath.Join(s.Data, name))
	}
	p, err := s.PlanPolicy(t.Context(), CachePolicy{time.Nanosecond, 1}, time.Now())
	if err != nil || len(p.Items) != 0 {
		t.Fatalf("personal files selected: %+v, %v", p, err)
	}
}

func TestCachePolicySafePathsAndCancellation(t *testing.T) {
	s := testStore(t)
	outside := filepath.Join(t.TempDir(), "keep")
	put(t, outside)
	if err := os.MkdirAll(filepath.Join(s.Data, "cache/addons"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(s.Data, "cache/addons/link")); err != nil {
		t.Fatal(err)
	}
	policy := CachePolicy{time.Nanosecond, 1}
	p, err := s.PlanPolicy(t.Context(), policy, time.Now())
	if err != nil || len(p.Items) != 0 || len(p.Skipped) != 1 {
		t.Fatalf("symlink plan=%+v, %v", p, err)
	}
	assertExists(t, outside, true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	policyFile(t, filepath.Join(s.Data, "cache/addons/a.yaml"), 10, time.Now())
	if _, err := s.PlanPolicy(ctx, policy, time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	for _, policy := range []CachePolicy{{0, 1}, {-1, 0}, {time.Hour, -1}} {
		if _, err := s.PlanPolicy(t.Context(), policy, time.Now()); err == nil {
			t.Fatal("accepted invalid policy")
		}
	}
}
