package main

import (
	"fmt"
	"io"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/runtime"
	"astrona/internal/ui"
)

// maxParallel bounds --parallel: every cluster at once is a lot of memory.
const maxParallel = 5

// scheduleHooks are called on the scheduling goroutine only — safe to
// touch a Reporter from.
type scheduleHooks struct {
	started func(i int)
	done    func(i int, err error, took time.Duration)
}

// scheduleKindClusters runs start for every cluster in order (start order from
// config.KindClusterOrder), at most parallel at a time, each only once every
// cluster it dependsOn has succeeded. After the first failure nothing new
// starts; the ones already running finish. Returns the indices never
// started and the first error.
func scheduleKindClusters(order []config.KindCluster, parallel int, start func(i int) error, hooks scheduleHooks) ([]int, error) {
	type result struct {
		i    int
		err  error
		took time.Duration
	}
	index := map[string]int{}
	for i, l := range order {
		index[l.Name] = i
	}
	started := make([]bool, len(order))
	succeeded := make([]bool, len(order))
	results := make(chan result)
	running := 0
	var firstErr error

	ready := func(i int) bool {
		for _, d := range order[i].DependsOn {
			if !succeeded[index[d]] {
				return false
			}
		}
		return true
	}

	for {
		if firstErr == nil {
			for i := range order {
				if running >= parallel {
					break
				}
				if started[i] || !ready(i) {
					continue
				}
				started[i] = true
				running++
				if hooks.started != nil {
					hooks.started(i)
				}
				go func(i int) {
					t0 := time.Now()
					err := start(i)
					results <- result{i, err, time.Since(t0)}
				}(i)
			}
		}
		if running == 0 {
			break
		}
		r := <-results
		running--
		if hooks.done != nil {
			hooks.done(r.i, r.err, r.took)
		}
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		succeeded[r.i] = true
	}

	var skipped []int
	for i := range order {
		if !started[i] {
			skipped = append(skipped, i)
		}
	}
	return skipped, firstErr
}

// startKindClustersParallel is startKindClusters with up to parallel clusters
// created at once. Each one logs to its own run log (its output would
// interleave on screen); rep only prints when one starts, is ready or
// fails — always from this goroutine.
func startKindClustersParallel(cfg *config.LabConfig, order []config.KindCluster, states []cluster.LinkState, baseDir string, forTest bool, parallel int, rep *ui.Reporter) error {
	byName := map[string]cluster.LinkState{}
	for _, s := range states {
		byName[s.Name] = s
	}
	// Each cluster's reporter (and so its log path) exists before any
	// goroutine starts: hooks read the paths, workers only use their own.
	subReps := make([]*ui.Reporter, len(order))
	logs := make([]string, len(order))
	for i := range order {
		r, err := ui.New("linked", states[i].Cluster, ui.Options{Screen: io.Discard})
		if err != nil {
			for _, prev := range subReps[:i] {
				prev.Close()
			}
			return err
		}
		subReps[i], logs[i] = r, r.LogPath()
	}
	defer func() {
		for _, r := range subReps {
			r.Close()
		}
	}()

	start := func(i int) error {
		l, name, subRep := order[i], states[i].Cluster, subReps[i]
		sub := kindClusterConfig(cfg, l)
		var deps []cluster.LinkState
		for _, d := range l.DependsOn {
			deps = append(deps, byName[d])
		}
		if err := runtime.DestroyEnvironment(name, sub.Runtime, subRep); err != nil {
			subRep.Warn("could not clean up a previous '%s', proceeding anyway: %s", name, err)
		}
		if _, _, err := upLab(sub, baseDir, name, deps, forTest, subRep); err != nil {
			return err
		}
		return applyWANStep(name, l.WAN, subRep)
	}

	rep.Section("Linked clusters (up to %d at once)", parallel)
	skipped, firstErr := scheduleKindClusters(order, parallel, start, scheduleHooks{
		started: func(i int) {
			rep.Info("Linked cluster '%s': creating %s (log: %s)", order[i].Name, states[i].Cluster, logs[i])
		},
		done: func(i int, err error, took time.Duration) {
			if err != nil {
				rep.Result(false, took, "Linked cluster '%s' (%s)", order[i].Name, states[i].Cluster)
				rep.Warn("%s — full log: %s", err, logs[i])
				return
			}
			rep.Result(true, took, "Linked cluster '%s' ready (%s)", order[i].Name, states[i].Cluster)
		},
	})
	if firstErr == nil {
		return nil
	}
	rest := make([]config.KindCluster, 0, len(skipped))
	for _, i := range skipped {
		rest = append(rest, order[i])
	}
	return fmt.Errorf("linked clusters failed: %w — not started: %s", firstErr, notStarted(rest))
}
