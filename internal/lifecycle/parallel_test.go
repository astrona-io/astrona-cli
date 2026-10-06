package lifecycle

import (
	"astrona/internal/config"
	"astrona/internal/ui"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestScheduleKindClustersRespectsDependenciesAndLimit(t *testing.T) {
	// db and cache are independent; idp needs db; app needs idp and cache.
	order, err := config.KindClusterOrder([]config.KindCluster{
		{Name: "app", DependsOn: []string{"idp", "cache"}},
		{Name: "idp", DependsOn: []string{"db"}},
		{Name: "db"},
		{Name: "cache"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	finished := map[string]bool{}
	var running, peak atomic.Int32
	start := func(i int) error {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		mu.Lock()
		for _, d := range order[i].DependsOn {
			if !finished[d] {
				t.Errorf("%s started before its dependency %s finished", order[i].Name, d)
			}
		}
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		finished[order[i].Name] = true
		mu.Unlock()
		running.Add(-1)
		return nil
	}
	skipped, err := scheduleClusters(order, 2, start, scheduleHooks{})
	if err != nil || len(skipped) != 0 {
		t.Fatalf("err = %v, skipped = %v", err, skipped)
	}
	if peak.Load() != 2 {
		t.Errorf("peak concurrency = %d, want 2 (db and cache together, limit 2)", peak.Load())
	}
	if len(finished) != 4 {
		t.Errorf("finished = %v", finished)
	}
}

func TestScheduleKindClustersStopsAfterFailure(t *testing.T) {
	order, _ := config.KindClusterOrder([]config.KindCluster{
		{Name: "db"},
		{Name: "idp", DependsOn: []string{"db"}},
		{Name: "cache"},
		{Name: "late"},
	})
	boom := errors.New("db broke")
	var startedNames []string
	skipped, err := scheduleClusters(order, 2, func(i int) error {
		if order[i].Name == "db" {
			return boom
		}
		time.Sleep(20 * time.Millisecond)
		return nil
	}, scheduleHooks{started: func(i int) { startedNames = append(startedNames, order[i].Name) }})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	var skippedNames []string
	for _, i := range skipped {
		skippedNames = append(skippedNames, order[i].Name)
	}
	// db and cache start together; db fails, so nothing new starts: idp
	// (depends on db) and late are skipped, cache finishes.
	if !slices.Equal(startedNames, []string{"db", "cache"}) || !slices.Contains(skippedNames, "idp") || !slices.Contains(skippedNames, "late") {
		t.Errorf("started = %v, skipped = %v", startedNames, skippedNames)
	}
}

// A --parallel failure names the linked cluster, like the sequential path.
func TestStartLinkedClustersParallelWrapsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	scripts := map[string]string{
		"docker":  "#!/bin/sh\nexit 0\n",
		"kubectl": "#!/bin/sh\necho user-ctx\n",
		// delete (cleanup of a previous run) succeeds; create fails.
		"kind": "#!/bin/sh\n[ \"$1\" = delete ] && exit 0\necho 'kind create failed' >&2\nexit 1\n",
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	cfg := &config.LabConfig{
		Metadata: config.MetadataConfig{Name: "lab"},
		Runtime:  config.RuntimeConfig{Kind: &config.KindConfig{Clusters: []config.KindCluster{{Name: "db"}, {Name: "cache"}}}},
	}
	_, err := StartLinkedClusters(cfg, t.TempDir(), "astro-lab", false, 2, ui.Discard())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "linked cluster 'db' (astro-lab-db): ") && !strings.Contains(err.Error(), "linked cluster 'cache' (astro-lab-cache): ") {
		t.Fatalf("error doesn't name the failed linked cluster: %v", err)
	}
}
