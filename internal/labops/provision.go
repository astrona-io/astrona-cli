package labops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"astrona/internal/account"
	"astrona/internal/apphome"
	"astrona/internal/cluster"
	"astrona/internal/config"
	"astrona/internal/exam"
	"astrona/internal/hypervisor"
	"astrona/internal/labshell"
	"astrona/internal/labstate"
	"astrona/internal/lifecycle"
	"astrona/internal/portforward"
	"astrona/internal/preflight"
	"astrona/internal/resources"
	"astrona/internal/runtime"
	"astrona/internal/ui"
)

// LabExists reports whether any part of cfg's environment exists under
// clusterName: the kind cluster, the single qemu VM, or any VM of a
// multi-VM qemu lab. A kind lab whose container engine doesn't answer is an
// error, not "absent": run and reset would otherwise end its session and
// try to create it over the one still there.
func LabExists(cfg *config.LabConfig, clusterName string) (bool, error) {
	if cfg.Runtime.Type == string(runtime.RuntimeQEMU) {
		if hypervisor.StateExists(clusterName) {
			return true, nil
		}
		for _, vm := range cfg.Runtime.QEMU {
			if vm.Name != "" && hypervisor.StateExists(clusterName+"-"+vm.Name) {
				return true, nil
			}
		}
		return false, nil
	}
	ok, err := cluster.Lookup(clusterName)
	if err != nil {
		return false, fmt.Errorf("cannot tell whether lab '%s' exists: %w\nStart Docker or Podman and try again", clusterName, err)
	}
	return ok, nil
}

// OtherRunningLabs are running kind labs other than except (no linked
// clusters, no test copies, no stopped ones), without the cluster prefix.
func OtherRunningLabs(except string) []string {
	owners := LinkedClusterOwners()
	var out []string
	for _, r := range CollectKindRows() {
		if _, linked := owners[r.Name]; linked || r.Name == except || strings.HasPrefix(r.Name, apphome.TestClusterPrefix()) || r.Status == "Stopped" {
			continue
		}
		out = append(out, strings.TrimPrefix(r.Name, apphome.ClusterPrefix()))
	}
	return out
}

// OtherRunningLabNames are the running labs — kind clusters and qemu VMs —
// other than clusterName and its own VMs and linked clusters, as cluster
// names. Test copies and stopped clusters are left out.
func OtherRunningLabNames(clusterName string) []string {
	var out []string
	for _, name := range OtherRunningLabs(clusterName) { // kind; already without linked clusters
		out = append(out, config.NormalizeClusterName(name))
	}
	rows, _, err := CollectQEMURows()
	if err != nil {
		return out
	}
	return append(out, OtherQEMULabs(clusterName, rows)...)
}

// OtherQEMULabs are the labs of the running qemu VMs in rows, other than
// clusterName — one name per lab, so a multi-VM lab ("astro-net" with VMs
// astro-net-vm1, astro-net-vm2) is destroyed and forgotten as the lab it
// is. Test copies are left out.
func OtherQEMULabs(clusterName string, rows []LabRow) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rows {
		lab := r.Lab
		if lab == "" {
			// A handle from before VMs recorded their lab: a VM named
			// "<clusterName>-…" may be one of this lab's own, so leave it be
			// (it can't be told from an unrelated lab with a longer name).
			if strings.HasPrefix(r.Name, clusterName+"-") {
				continue
			}
			lab = r.Name
		}
		if lab == clusterName || strings.HasPrefix(lab, apphome.TestClusterPrefix()) || seen[lab] {
			continue
		}
		seen[lab] = true
		out = append(out, lab)
	}
	return out
}

// RemoveForRestart tears down the running lab clusterName before it is
// built again from cfg (run's start over, a full reset): its teardown
// scripts unless skipScripts, then the environment — teardown.keepCluster
// is ignored. With keepContext (run won't switch kubectl again) the
// context the old lab switched away from is put back first, said on out.
// The error is the teardown's own; the caller says what was not started.
func RemoveForRestart(out io.Writer, cfg *config.LabConfig, baseDir, clusterName string, keepContext, skipScripts bool, rep *ui.Reporter) error {
	if keepContext {
		ReleaseLab(out, clusterName)
	}
	teardown := cfg.Teardown
	teardown.KeepCluster = false
	info := Teardown{ClusterName: clusterName, Teardown: teardown, Runtime: cfg.Runtime, SkipScripts: skipScripts}
	return TearDownLabEnvironment(clusterName, info, baseDir, true, rep)
}

// ProvisionRequest is what Provision builds and how. cmd fills it from its
// flags; labops never reads them.
type ProvisionRequest struct {
	Config  *config.LabConfig
	BaseDir string
	// Source is where the lab's config came from, remembered for submit and
	// destroy once the lab is up.
	Source *labstate.Source
	// LocalOnly marks a UI restart: no account session, progress or
	// results.
	LocalOnly bool
	// KeepContext leaves the user's kubectl current-context alone.
	KeepContext bool
	// Parallel is how many linked clusters are created at once (--parallel).
	Parallel int
	// Session is the lab session the lab runs under (nil: none).
	Session *labstate.Session
	// ConfigChecks are the lab config checks preflight runs.
	ConfigChecks preflight.ConfigChecker
	// Warn gets warnings and the diagnostics summary after a failure —
	// stderr in cmd.
	Warn io.Writer
	// Context stops the build when cancelled (Ctrl+C in cmd): what this
	// run created is removed and Provision returns ErrInterrupted. nil:
	// never cancelled.
	Context context.Context
}

// ErrInterrupted is returned by an operation stopped by its context
// (Ctrl+C) — after it undid what it had started.
var ErrInterrupted = errors.New("interrupted")

// removeInterrupted removes what an interrupted Provision of clusterName
// created — the lab's environment, its linked clusters, port forwards, the
// copy of its resources and its terminal workspace — and returns
// ErrInterrupted. Provision only gets this far once the clusters it starts
// were absent or replaced, so nothing here existed before the run.
func removeInterrupted(cfg *config.LabConfig, clusterName, baseDir string, rep *ui.Reporter) error {
	rep.Section("Interrupted — removing what this run created")
	// No teardown scripts: the lab never got to run its bootstrap through.
	info := Teardown{ClusterName: clusterName, Runtime: cfg.Runtime, SkipScripts: true}
	if err := TearDownLabEnvironment(clusterName, info, baseDir, false, rep); err != nil {
		rep.Warn("could not remove lab %s: %s — run `astrona destroy %s`", clusterName, err, clusterName)
	}
	// The lab's own environment may never have existed (TearDownLabEnvironment
	// then stops early): its linked clusters and forwards still go.
	portforward.StopForLab(clusterName, rep)
	lifecycle.DestroyOwnedClusters(lifecycle.OwnedClusters(clusterName, cfg.KindClusters()), rep)
	if err := resources.Remove(clusterName); err != nil {
		rep.Warn("could not remove the lab's resources copy: %s", err)
	}
	_ = labshell.Remove(context.Background(), clusterName)
	return fmt.Errorf("%w: lab %s was not started", ErrInterrupted, clusterName)
}

// ProvisionResult is what Provision built, for cmd to print how to reach
// the lab.
type ProvisionResult struct {
	ClusterName string
	Env         *runtime.LabEnvironment
	Forwards    []portforward.Forward
	Links       []cluster.LinkState
	// Switched is the kubectl context switch (nil: not switched — kept, a
	// qemu lab, or the switch failed with a warning).
	Switched     *labstate.Switched
	ResourcesDir string
	Resources    []resources.Resource
	// ExamStarted is set when the lab's exam clock runs.
	ExamStarted bool
	LogPath     string
}

// Provision creates the request's lab: the environment preflight, its
// linked clusters (runtime.kind.clusters), then the lab itself with
// everything `astrona run` does — preload, addons, bootstrap, manifests,
// readiness gates, port forwards. A failure collects diagnostics. Once the
// lab is ready its exam clock starts, where it came from and its lab
// session are remembered, rep is closed, and a kind lab's context becomes
// the user's kubectl current-context (unless KeepContext). Shared by run
// and reset; the caller holds the lab's mutation lock.
func Provision(req ProvisionRequest, rep *ui.Reporter) (*ProvisionResult, error) {
	cfg, baseDir := req.Config, req.BaseDir
	clusterName := config.NormalizeClusterName(cfg.Metadata.Name)
	ctx := req.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("%w: lab %s was not started", ErrInterrupted, clusterName)
	}
	if err := preflight.RunEnvironment(cfg, baseDir, rep, req.ConfigChecks); err != nil {
		return nil, err
	}
	// Checked before linked clusters are (re)created — they'd otherwise be
	// replaced under a lab that's still using them.
	if len(cfg.KindClusters()) > 0 && cluster.Exists(clusterName) {
		return nil, fmt.Errorf("lab %s is already running — `astrona reset` starts it over", clusterName)
	}
	// Resources are collected and copied before anything is built, so a
	// broken resources/ folder costs no cluster. Nothing in them runs.
	res, err := resources.Collect(cfg, baseDir)
	if err != nil {
		return nil, err
	}
	if err := labshell.Remove(context.Background(), clusterName); err != nil {
		return nil, fmt.Errorf("clear the previous terminal workspace: %w", err)
	}
	resDir, err := resources.Snapshot(clusterName, baseDir, res)
	if err != nil {
		return nil, fmt.Errorf("could not copy the lab's resources: %w", err)
	}
	if err := lifecycle.PrepareSharedCA(cfg, clusterName); err != nil {
		return nil, err
	}
	// From here on, Ctrl+C removes what this run created (removeInterrupted)
	// instead of leaving it for diagnosis.
	links, err := lifecycle.StartLinkedClusters(ctx, cfg, baseDir, clusterName, false, req.Parallel, rep)
	if ctx.Err() != nil {
		return nil, removeInterrupted(cfg, clusterName, baseDir, rep)
	}
	if err != nil {
		collectRunLinkedFailure(req.Warn, cfg, clusterName, rep)
		return nil, err
	}
	env, forwards, err := lifecycle.Up(ctx, cfg, baseDir, clusterName, links, false, rep)
	if ctx.Err() != nil {
		return nil, removeInterrupted(cfg, clusterName, baseDir, rep)
	}
	if err != nil {
		if env != nil {
			CollectDiagnostics(req.Warn, env, cfg, clusterName, "", rep)
		}
		return nil, err
	}

	// The clock starts once the lab is ready — setup time doesn't count.
	if cfg.Exam.Enabled() {
		if err := exam.Start(clusterName, cfg.Exam.Limit(), time.Now()); err != nil {
			rep.Warn("could not start the exam clock: %s", err)
		}
	}

	if err := RememberLab(clusterName, req.Source, req.LocalOnly, req.Session); err != nil {
		ui.Fwarnf(req.Warn, "could not remember this lab (%s) — `astrona submit` needs it named, and results won't reach the lab page", err)
	}
	rep.Close()
	var sw *labstate.Switched
	if env.Type == runtime.RuntimeKind && !req.KeepContext {
		if sw, err = SwitchToLab(clusterName, env.KubeContext); err != nil {
			ui.Fwarnf(req.Warn, "kubectl was not switched to this lab: %s", err)
		}
	}
	return &ProvisionResult{
		ClusterName:  clusterName,
		Env:          env,
		Forwards:     forwards,
		Links:        links,
		Switched:     sw,
		ResourcesDir: resDir,
		Resources:    res,
		ExamStarted:  cfg.Exam.Enabled(),
		LogPath:      rep.LogPath(),
	}, nil
}

// LabStart is the lab session a run or a full reset builds a lab under —
// none for a lab that isn't from the catalog. Shared by run and reset, so
// a reset ends exactly like a fresh run.
type LabStart struct {
	Page    *url.URL
	Session *labstate.Session
	ready   bool
	// interrupted: see Interrupted.
	interrupted bool
	out         io.Writer
	warn        io.Writer
}

// Interrupted marks the lab's setup as stopped by the user (Ctrl+C), so
// Abandon closes its session instead of leaving it open for a retry.
func (ls *LabStart) Interrupted() {
	if ls != nil {
		ls.interrupted = true
	}
}

// StartLabSession starts acct's session for the catalog lab (nil acct:
// none) before anything is built — a timed playground for a module's
// playground, with the config's time limit. out gets what the user is
// told (stdout in cmd), warn the warnings (stderr).
func StartLabSession(ctx context.Context, out, warn io.Writer, acct *Account, command string, cfg *config.LabConfig, lab string) (*LabStart, error) {
	ls := &LabStart{out: out, warn: warn}
	if acct == nil {
		return ls, nil
	}
	var opts []account.SessionOptions
	if IsPlaygroundName(lab) {
		minutes, _ := cfg.Metadata.TimeLimitMinutes() // checked by lifecycle.Validate
		opts = append(opts, account.SessionOptions{Kind: "playground", TimeLimitMinutes: minutes})
	}
	var err error
	ls.Page, ls.Session, err = StartSession(ctx, out, warn, acct, command, lab, opts...)
	return ls, err
}

// Abandon stops a playground's clock when the playground never became
// ready (deferred right after StartLabSession) — it started with the
// session and would otherwise run on to the deadline with nothing to show
// for it. A Lab has no timer until its course page connects; it is left
// open after a failure (the page shows it failed, a retry continues it),
// but one the user interrupted (Interrupted) is closed as destroyed, as
// `astrona destroy` would.
func (ls *LabStart) Abandon() {
	if ls.ready {
		return
	}
	if ls.interrupted {
		ls.Progress("failed", "Lab setup was interrupted")
	} else {
		ls.Progress("failed", "Lab setup stopped before it was ready")
	}
	if !ls.Session.IsPlayground() {
		if ls.interrupted && ls.Session != nil {
			EndLabSession(context.Background(), ls.warn, ls.Session)
		}
		return
	}
	StopPlaygroundClock(context.Background(), ls.warn, ls.Session, "destroyed", nil)
}

// Done follows a lab that is ready: a playground's watchdog starts (its
// clock is already running), and a Lab reports that the course page may
// connect.
func (ls *LabStart) Done(clusterName string) {
	ls.ready = true
	ls.Progress("ready", "Lab environment is ready")
	if ls.Session != nil && ls.Session.ID != "" {
		StartUsageReporter(ls.warn, clusterName)
	}
	if ls.Session.IsPlayground() {
		StartPlaygroundWatchdog(ls.out, ls.warn, clusterName, ls.Session)
		return
	}
	if ls.Page != nil {
		fmt.Fprintf(ls.out, "\nLab ready. Continue in your course page, or open: %s\n", ls.Page)
		WriteTimeLimit(ls.out, ls.Session)
	}
}

// Progress reports a Lab's setup phase to its lab page, best effort.
func (ls *LabStart) Progress(phase, message string) {
	if ls == nil || ls.Session == nil || ls.Session.IsPlayground() {
		return
	}
	client, store, _, err := AccountDeps()
	if err != nil {
		return
	}
	creds, err := client.Active(context.Background(), store, ls.Session.Site)
	if err != nil || creds == nil {
		return
	}
	_ = client.UpdateLabSessionProgress(context.Background(), creds, ls.Session.ID, phase, message)
}

// WriteTimeLimit says on w how long the attempt runs once the lab page
// opens, when the site gave a limit.
func WriteTimeLimit(w io.Writer, sess *labstate.Session) {
	if sess != nil && sess.MaxMinutes > 0 {
		fmt.Fprintf(w, "You have %d minutes once the lab page opens.\n", sess.MaxMinutes)
	}
}

// SoftReset brings the running lab back to its just-started state without
// recreating its clusters (only the linked cluster named linked, when
// set): its lab namespaces and what isn't astrona's in default are
// deleted, then its bootstrap runs again. It returns the restarted port
// forwards.
func SoftReset(cfg *config.LabConfig, baseDir, clusterName, linked string, rep *ui.Reporter) ([]portforward.Forward, error) {
	if err := lifecycle.PrepareSharedCA(cfg, clusterName); err != nil { // the lab's existing CA
		return nil, err
	}
	return lifecycle.SoftReset(cfg, baseDir, clusterName, linked, rep)
}

// LinkedResetPlan is one linked cluster of a running lab, found before the
// user is asked to rebuild it.
type LinkedResetPlan struct {
	Lab     string // the lab's cluster name
	Target  string // the linked cluster's own cluster name
	cluster config.KindCluster
	states  []cluster.LinkState
}

// PlanLinkedReset finds the linked cluster name (its runtime.kind.clusters
// name) of the running lab clusterName.
func PlanLinkedReset(cfg *config.LabConfig, clusterName, name string) (*LinkedResetPlan, error) {
	order, states, err := lifecycle.ClusterStates(cfg, clusterName)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, l := range order {
		if l.Name == name {
			idx = i
		}
	}
	if idx < 0 {
		_, err := lifecycle.FindLinkedCluster(clusterName, name, states)
		return nil, err
	}
	if !cluster.Exists(clusterName) {
		return nil, fmt.Errorf("lab %s isn't running — `astrona run` creates it with all its clusters", clusterName)
	}
	return &LinkedResetPlan{Lab: clusterName, Target: states[idx].Cluster, cluster: order[idx], states: states}, nil
}

// ResetLinkedCluster rebuilds plan's linked cluster: teardown scripts,
// destroy, then create + bootstrap it again from the config, with its
// dependencies' addresses. Port forwards into it are restarted (their
// supervisors give up when the cluster goes away) and returned. Cancelling
// ctx (Ctrl+C) during the rebuild removes the half-built linked cluster —
// the lab's other clusters stay — and returns ErrInterrupted.
func ResetLinkedCluster(ctx context.Context, cfg *config.LabConfig, baseDir string, plan *LinkedResetPlan, rep *ui.Reporter) ([]portforward.Forward, error) {
	clusterName, l, target := plan.Lab, plan.cluster, plan.Target
	lifecycle.RunLinkedTeardown([]config.KindCluster{l}, clusterName, baseDir, rep)
	if cluster.Exists(target) {
		if err := lifecycle.DestroyKindCluster(target, rep); err != nil {
			return nil, fmt.Errorf("could not remove linked cluster %s, nothing was recreated: %w", target, err)
		}
	}

	if err := lifecycle.PrepareSharedCA(cfg, clusterName); err != nil { // the lab's existing CA
		return nil, err
	}
	byName := map[string]cluster.LinkState{}
	for _, s := range plan.states {
		byName[s.Name] = s
	}
	var deps []cluster.LinkState
	for _, d := range l.DependsOn {
		deps = append(deps, byName[d])
	}
	rep.Section("Linked cluster '%s'", l.Name)
	_, _, err := lifecycle.Up(ctx, lifecycle.LinkedClusterConfig(cfg, l), baseDir, target, deps, false, rep)
	if ctx.Err() != nil {
		rep.Section("Interrupted — removing linked cluster %s", target)
		if cluster.Exists(target) {
			if err := lifecycle.DestroyKindCluster(target, rep); err != nil {
				rep.Warn("could not remove linked cluster %s: %s — run `astrona destroy %s`", target, err, clusterName)
			}
		}
		return nil, fmt.Errorf("%w: linked cluster '%s' was not recreated — `astrona reset --cluster %s` builds it again", ErrInterrupted, l.Name, l.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("linked cluster '%s' (%s): %w", l.Name, target, err)
	}
	if err := lifecycle.ApplyWAN(target, l.WAN, rep); err != nil {
		return nil, fmt.Errorf("linked cluster '%s' (%s): %w", l.Name, target, err)
	}
	// Its node has a new IP; every cluster of the lab needs to know it.
	lifecycle.RefreshLinkNames(clusterName, false, rep)

	var forwards []portforward.Forward
	for _, pf := range cfg.Runtime.PortForwards {
		if pf.Cluster == l.Name {
			rep.Section("Port forwards")
			var err error
			forwards, err = lifecycle.StartPortForwards(clusterName, cfg.Runtime.PortForwards, rep)
			if err != nil {
				rep.Warn("some port forwards could not be restarted — `astrona port-forward start -c <config>`")
			}
			break
		}
	}
	return forwards, nil
}
