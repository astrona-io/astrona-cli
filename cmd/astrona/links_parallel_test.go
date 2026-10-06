package main

import (
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"astrona/internal/config"
)

func TestScheduleKindLabsRespectsDependenciesAndLimit(t *testing.T) {
	// db and cache are independent; idp needs db; app needs idp and cache.
	order, err := config.KindLabOrder([]config.KindLab{
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
	skipped, err := scheduleKindLabs(order, 2, start, scheduleHooks{})
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

func TestScheduleKindLabsStopsAfterFailure(t *testing.T) {
	order, _ := config.KindLabOrder([]config.KindLab{
		{Name: "db"},
		{Name: "idp", DependsOn: []string{"db"}},
		{Name: "cache"},
		{Name: "late"},
	})
	boom := errors.New("db broke")
	var startedNames []string
	skipped, err := scheduleKindLabs(order, 2, func(i int) error {
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

func TestValidateParallel(t *testing.T) {
	for n, ok := range map[int]bool{0: false, 1: true, maxParallel: true, maxParallel + 1: false} {
		if (validateParallel(n) == nil) != ok {
			t.Errorf("validateParallel(%d) ok = %v", n, !ok)
		}
	}
}
