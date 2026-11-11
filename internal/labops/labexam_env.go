package labops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/lifecycle"
	"astrona/internal/runtime"
	"astrona/internal/ui"
)

// ExamLabLoader loads a task's catalog lab: its config and the directory its
// scripts and manifests are relative to. cleanup releases any checkout.
// The command layer provides it (catalog lookup, version and trust checks).
type ExamLabLoader func(lab string) (cfg *config.LabConfig, baseDir string, cleanup func(), err error)

// ExamPrepareRequest is what PrepareExam builds.
type ExamPrepareRequest struct {
	State   *ExamState
	Profile *config.KindConfig
	Load    ExamLabLoader
	// Only, when set, rebuilds just these cluster numbers (a resume whose
	// clusters are partly gone); otherwise every cluster is built.
	Only     []int
	Parallel int
	// Progress, when set, gets the page's line for each step ("Building
	// cluster 2 of 4", "Applying task 7 setup"). It is called from a
	// queue, never from the build: a slow site doesn't hold the build up,
	// and the last step is sent again during a long one (keepalive). task
	// is set (non-zero) on the line that says a task's setup was applied,
	// so labs records when it was (the task's setup_at).
	Progress func(step string, task int)
	Rep      *ui.Reporter
}

// examTaskLab is a task with its loaded lab.
type examTaskLab struct {
	task    ExamTaskRef
	cfg     *config.LabConfig
	baseDir string
}

// examLabFits refuses a lab that can't run inside an exam cluster: it must
// be a kind lab without linked clusters. content-sync only lets eligible
// labs into the pool; this is the CLI's own guard.
func examLabFits(cfg *config.LabConfig) error {
	if cfg.Runtime.Type != "" && cfg.Runtime.Type != string(runtime.RuntimeKind) {
		return fmt.Errorf("it runs on %s, not kind", cfg.Runtime.Type)
	}
	if k := cfg.Runtime.Kind; k != nil && (len(k.Clusters) > 0 || len(k.Labs) > 0) {
		return errors.New("it has linked clusters")
	}
	return nil
}

// PrepareExam builds the attempt's clusters from the profile, up to
// Parallel at once, and applies each task's setup to its cluster: the
// cluster-scoped task first, then the others by position. Every lab is
// loaded and checked before anything is built. It stops at the first
// failure and leaves the clusters for the caller to delete (TeardownExam).
// Cancelling ctx stops it at the next step.
func PrepareExam(ctx context.Context, req ExamPrepareRequest) error {
	st, rep := req.State, req.Rep
	only := map[int]bool{}
	for _, n := range req.Only {
		only[n] = true
	}
	build := func(n int) bool { return len(only) == 0 || only[n] }

	// Load every lab first: a broken or untrusted lab fails here, before
	// minutes of building.
	rep.Section("Exam tasks")
	byCluster := map[int][]examTaskLab{}
	var cleanups []func()
	defer func() {
		for _, c := range cleanups {
			c()
		}
	}()
	for _, t := range st.Tasks {
		if !build(t.Cluster) {
			continue
		}
		if st.ClusterName(t.Cluster) == "" {
			return fmt.Errorf("task %d is on cluster %d, which this attempt doesn't have", t.Position, t.Cluster)
		}
		cfg, baseDir, cleanup, err := req.Load(t.Lab)
		if err != nil {
			return fmt.Errorf("task %d (%s): %w", t.Position, t.Lab, err)
		}
		cleanups = append(cleanups, cleanup)
		if err := lifecycle.Validate(cfg); err != nil {
			return fmt.Errorf("task %d (%s): %w", t.Position, t.Lab, err)
		}
		if err := examLabFits(cfg); err != nil {
			return fmt.Errorf("task %d (%s) can't be part of a hands-on exam: %w", t.Position, t.Lab, err)
		}
		byCluster[t.Cluster] = append(byCluster[t.Cluster], examTaskLab{task: t, cfg: cfg, baseDir: baseDir})
	}
	for n := range byCluster {
		tasks := byCluster[n]
		sort.SliceStable(tasks, func(i, j int) bool {
			ci, cj := tasks[i].task.Scope == "cluster", tasks[j].task.Scope == "cluster"
			if ci != cj {
				return ci
			}
			return tasks[i].task.Position < tasks[j].task.Position
		})
	}
	rep.Info("%d task(s) loaded", len(cleanups))

	var clusters []ExamCluster
	for _, c := range st.Clusters {
		if build(c.N) {
			clusters = append(clusters, c)
		}
	}
	total := len(st.Clusters)

	var q *stepQueue
	if req.Progress != nil {
		q = newStepQueue(func(s progressStep) { req.Progress(s.Message, s.Task) })
		defer q.close()
	}
	stepFor := func(task int, format string, a ...any) {
		if q != nil {
			q.add(progressStep{Message: fmt.Sprintf(format, a...), Task: task})
		}
	}
	step := func(format string, a ...any) { stepFor(0, format, a...) }

	// Each cluster logs to its own run log: their output would interleave
	// on screen. rep only prints from this goroutine (RunParallel's hooks).
	subReps := make([]*ui.Reporter, len(clusters))
	for i, c := range clusters {
		r, err := ui.New("exam", c.Name, ui.Options{Screen: io.Discard})
		if err != nil {
			for _, prev := range subReps[:i] {
				prev.Close()
			}
			return err
		}
		subReps[i] = r
	}
	defer func() {
		for _, r := range subReps {
			r.Close()
		}
	}()

	var stepMu sync.Mutex // the queue is safe; this keeps a cluster's lines together
	runtimeCfg := examRuntime(req.Profile)
	start := func(i int) error {
		c, sub := clusters[i], subReps[i]
		if err := ctx.Err(); err != nil {
			return err
		}
		stepMu.Lock()
		step("Building cluster %d of %d", c.N, total)
		stepMu.Unlock()
		if err := runtime.DestroyEnvironment(c.Name, runtimeCfg, sub); err != nil {
			return fmt.Errorf("cluster %d: remove the previous cluster: %w", c.N, err)
		}
		profileCfg := &config.LabConfig{Metadata: config.MetadataConfig{Name: c.Name}, Runtime: examRuntime(req.Profile)}
		if _, _, err := lifecycle.Up(ctx, profileCfg, "", c.Name, nil, true, sub); err != nil {
			return fmt.Errorf("cluster %d: %w", c.N, err)
		}
		for _, tl := range byCluster[c.N] {
			if err := ctx.Err(); err != nil {
				return err
			}
			step("Applying task %d setup", tl.task.Position)
			if err := applyExamTask(c.N, c.Name, runtimeCfg, tl, sub); err != nil {
				return fmt.Errorf("task %d (%s) setup on cluster %d: %w", tl.task.Position, tl.task.Lab, c.N, err)
			}
			stepFor(tl.task.Position, "Task %d is set up", tl.task.Position)
		}
		step("Cluster %d of %d is ready", c.N, total)
		return nil
	}

	rep.Section("Exam clusters (up to %d at once)", req.Parallel)
	_, err := lifecycle.RunParallel(len(clusters), req.Parallel, start,
		func(i int) {
			rep.Info("Cluster %d of %d: building %s (log: %s)", clusters[i].N, total, clusters[i].Name, subReps[i].LogPath())
		},
		func(i int, err error, took time.Duration) {
			if err != nil {
				rep.Result(false, took, "Cluster %d of %d (%s)", clusters[i].N, total, clusters[i].Name)
				rep.Warn("%s — full log: %s", err, subReps[i].LogPath())
				return
			}
			rep.Result(true, took, "Cluster %d of %d ready, %d task(s) set up", clusters[i].N, total, len(byCluster[clusters[i].N]))
		})
	if err != nil {
		return err
	}
	return ctx.Err()
}

// examEnvironment is exam cluster n (kind cluster clusterName) as a task's
// scripts and checks see it: KUBECONFIG is the cluster's exam kubeconfig,
// whose current-context is exam-<n>, ASTRONA_KUBE_CONTEXT is exam-<n> and
// ASTRONA_KIND_CLUSTER is clusterName. A lab's scripts refuse to run
// unless the current context is ASTRONA_KUBE_CONTEXT, so this is what lets
// them run on an exam cluster without weakening that guard. astrona's own
// kubectl calls stay pinned to kind-<clusterName>, which the exam
// kubeconfig also holds (and the student's kubeconfig, which bootstrap
// manifests use, has before exam-<n> is added).
func examEnvironment(n int, clusterName string, rt config.RuntimeConfig) (*runtime.LabEnvironment, error) {
	env, err := runtime.LoadEnvironment(clusterName, rt)
	if err != nil {
		return nil, err
	}
	kubeconfig, err := cluster.WriteExamKubeconfig(clusterName, ExamContext(n))
	if err != nil {
		return nil, err
	}
	env.UseScriptContext(ExamContext(n), kubeconfig)
	env.AddEnv(lifecycle.CAEnv(clusterName)...)
	return env, nil
}

// applyExamTask runs a task's setup on exam cluster n: the lab's shared
// CA when it has one (as a lab run installs it, before bootstrap), then
// its bootstrap (init scripts, manifests, readiness gates). A
// cluster-scoped task changes the control plane, so the API server is
// given time to settle before the next setup. The CA belongs to the exam
// cluster, so every task on it trusts the same one, and it goes away with
// the cluster.
func applyExamTask(n int, clusterName string, rt config.RuntimeConfig, tl examTaskLab, rep *ui.Reporter) error {
	env, err := examEnvironment(n, clusterName, rt)
	if err != nil {
		return err
	}
	rep.Section("Task %d: %s", tl.task.Position, tl.task.Lab)
	if err := lifecycle.PrepareSharedCA(tl.cfg, clusterName); err != nil {
		return err
	}
	if err := lifecycle.InstallSharedCA(tl.cfg, env, rep); err != nil {
		return err
	}
	if err := lifecycle.Bootstrap(tl.cfg, tl.baseDir, env, rep); err != nil {
		return err
	}
	if tl.task.Scope == "cluster" {
		return waitAPIServer(env.Kubeconfig, ExamContext(n), 3*time.Minute, rep)
	}
	return nil
}

// waitAPIServer waits until the API server of kubeContext in kubeconfig
// answers /readyz.
func waitAPIServer(kubeconfig, kubeContext string, limit time.Duration, rep *ui.Reporter) error {
	t := rep.Step("Wait for the API server to settle")
	kubectl, err := executor.LookKubectl()
	if err != nil {
		return t.Fail(err)
	}
	args := []string{"--kubeconfig", kubeconfig, "--context", kubeContext, "get", "--raw=/readyz", "--request-timeout=10s"}
	deadline := time.Now().Add(limit)
	var out []byte
	for {
		out, err = exec.Command(kubectl, args...).CombinedOutput()
		if err == nil {
			t.Done()
			return nil
		}
		if time.Now().After(deadline) {
			return t.Fail(fmt.Errorf("the API server is not ready after %s: %s", limit, strings.TrimSpace(string(out))))
		}
		time.Sleep(3 * time.Second)
	}
}

// MissingExamClusters are the attempt's cluster numbers whose kind cluster
// is gone (a reboot, a deleted container).
func MissingExamClusters(st *ExamState) []int {
	var missing []int
	for _, c := range st.Clusters {
		if !cluster.Exists(c.Name) {
			missing = append(missing, c.N)
		}
	}
	return missing
}

// kubectlContexts is the user's kubeconfig (replaced in tests).
var kubectlContexts = cluster.NewKubectlContexts

// CheckExamContexts refuses an attempt whose exam-<n> contexts already exist
// in the user's kubeconfig for something other than an exam cluster: they
// are the student's own, and preparation would overwrite them.
func CheckExamContexts(count int) error {
	kc, err := kubectlContexts()
	if err != nil {
		return err
	}
	for n := 1; n <= count; n++ {
		name := ExamContext(n)
		clusterRef, err := kc.ContextCluster(name)
		if err != nil {
			return err
		}
		if clusterRef != "" && !IsExamCluster(strings.TrimPrefix(clusterRef, "kind-")) {
			return fmt.Errorf("your kubeconfig already has a context %s (cluster %s); the exam uses that name. Rename or delete it first (kubectl config rename-context %s <new-name>)", name, clusterRef, name)
		}
	}
	return nil
}

// AddExamContexts records the current context (once per attempt) and adds
// exam-1 … exam-X to the user's kubeconfig, each pointing at the kind
// entries `kind create` merged there. The current context is not changed:
// switching is part of what the exam rehearses.
func AddExamContexts(st *ExamState) error {
	kc, err := kubectlContexts()
	if err != nil {
		return err
	}
	cur, set, err := kc.Current()
	if err != nil {
		return fmt.Errorf("could not read your kubectl current-context: %w", err)
	}
	if err := UpdateExamState(func(s *ExamState) (*ExamState, error) {
		if s == nil || s.ID != st.ID {
			return s, nil
		}
		if s.PreviousContext == "" && !s.PreviousUnset && !isExamContextName(s, cur) {
			s.PreviousContext, s.PreviousUnset = cur, !set
		}
		st.PreviousContext, st.PreviousUnset = s.PreviousContext, s.PreviousUnset
		return s, nil
	}); err != nil {
		return err
	}
	for _, c := range st.Clusters {
		if err := kc.SetContext(ExamContext(c.N), "kind-"+c.Name, "kind-"+c.Name); err != nil {
			return err
		}
	}
	return nil
}

// isExamContextName reports whether name is one of the attempt's contexts
// (exam-<n> or kind-<its cluster>).
func isExamContextName(st *ExamState, name string) bool {
	for _, c := range st.Clusters {
		if name == ExamContext(c.N) || name == "kind-"+c.Name {
			return true
		}
	}
	return false
}

// PauseExam pauses (on false: unpauses) the node containers of every
// cluster of the attempt. A failure on one cluster doesn't stop the rest;
// the errors are joined.
func PauseExam(st *ExamState, pause bool) error {
	var errs []error
	for _, c := range st.Clusters {
		var err error
		if pause {
			err = cluster.PauseKindCluster(c.Name)
		} else {
			err = cluster.UnpauseKindCluster(c.Name)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("cluster %d: %w", c.N, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return UpdateExamState(func(s *ExamState) (*ExamState, error) {
		if s != nil && s.ID == st.ID {
			s.Paused = pause
		}
		return s, nil
	})
}

// TeardownExam deletes every cluster of the attempt and its exam-<n>
// contexts, and puts back the kubectl context the student had before when
// they left it on an exam context. The local state stays, marked Ended,
// while results wait in Pending; otherwise it is removed.
func TeardownExam(st *ExamState, rep *ui.Reporter) error {
	rep.Section("Exam teardown")
	var errs []error
	// A paused container can't be stopped cleanly everywhere.
	_ = PauseExam(st, false)
	for _, c := range st.Clusters {
		if !cluster.Exists(c.Name) {
			continue
		}
		if err := lifecycle.DestroyKindCluster(c.Name, rep); err != nil {
			errs = append(errs, fmt.Errorf("cluster %d (%s): %w", c.N, c.Name, err))
		}
	}
	if kc, err := kubectlContexts(); err == nil {
		cur, set, curErr := kc.Current()
		for _, c := range st.Clusters {
			if err := kc.DeleteContext(ExamContext(c.N)); err != nil {
				errs = append(errs, err)
			}
		}
		// Put the old context back when the student is left on an exam
		// context, or on none because deleting a kind cluster unset its
		// context, but never when they had none before and have none now.
		onExam := set && isExamContextName(st, cur)
		unsetByDelete := !set && !st.PreviousUnset
		if curErr == nil && (onExam || unsetByDelete) {
			switch {
			case st.PreviousUnset:
				_ = kc.Unset()
			case st.PreviousContext != "":
				if ok, _ := kc.Exists(st.PreviousContext); ok {
					if err := kc.Use(st.PreviousContext); err != nil {
						rep.Warn("could not switch kubectl back to %s: %s", st.PreviousContext, err)
					} else {
						rep.Info("kubectl context switched back to %s", st.PreviousContext)
					}
				}
			}
		}
	} else {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return UpdateExamState(func(s *ExamState) (*ExamState, error) {
		if s == nil || s.ID != st.ID {
			return s, nil
		}
		if len(s.Pending) == 0 {
			return nil, nil
		}
		s.Ended, s.Paused, s.WatchdogPID = true, false, 0
		return s, nil
	})
}
