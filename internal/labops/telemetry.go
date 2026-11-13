package labops

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"astrona/internal/account"
	"astrona/internal/cluster"
	"astrona/internal/telemetry"
	"astrona/internal/ui"
)

// The CLI's usage analytics (internal/telemetry): what a run, a submit and
// a teardown record, and how the waiting events are sent. Every function
// here is best effort and silent: telemetry never changes what a command
// does or prints.

func init() {
	telemetry.SignedIn = signedInLocally
}

// signedInLocally reports, from the saved credentials only (no network),
// whether this computer is signed in to the site events go to.
func signedInLocally() bool {
	_, store, site, err := AccountDeps()
	if err != nil {
		return false
	}
	cr, err := store.Load()
	return err == nil && cr != nil && cr.Site == site
}

// RunTelemetry follows one `astrona run` for its run_started and
// run_finished events. A nil *RunTelemetry (telemetry off) does nothing.
type RunTelemetry struct {
	cluster   string
	run       *telemetry.Run
	mu        sync.Mutex
	readiness time.Time // when the Readiness section began
}

// StartRunTelemetry records run_started for a run of cluster. catalog is the
// catalog lab ("" for a lab from a local folder: its path is never sent);
// runtimeType is the config's runtime.type.
func StartRunTelemetry(clusterName, catalog string, playground bool, runtimeType string) *RunTelemetry {
	if !telemetry.Current().Enabled {
		return nil
	}
	r := telemetry.Run{Lab: catalog, Kind: "lab", Target: "kind"}
	if playground {
		r.Kind = "playground"
	}
	if runtimeType == "qemu" {
		r.Target, r.Runtime = "vm-terminal", "qemu"
	} else {
		r.Runtime, r.RuntimeMajor = containerRuntime()
	}
	run := telemetry.StartRun(clusterName, r)
	if run == nil {
		return nil
	}
	telemetry.Record(run.Event(telemetry.RunStarted))
	return &RunTelemetry{cluster: clusterName, run: run}
}

// Observe notes when the lab's Readiness section starts: before it the run
// provisions, after it the lab becomes ready.
func (t *RunTelemetry) Observe(e ui.Event) {
	if t == nil || e.Kind != "section" || e.Label != "Readiness" {
		return
	}
	t.mu.Lock()
	if t.readiness.IsZero() {
		t.readiness = time.Now()
	}
	t.mu.Unlock()
}

// Finish records run_finished: ready when err is nil, else cancelled
// (interrupted) or failed with the error's class. A run that did not end
// ready leaves no lab, so its run is forgotten.
func (t *RunTelemetry) Finish(err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	readiness := t.readiness
	t.mu.Unlock()
	now := time.Now()
	ev := t.run.Event(telemetry.RunFinished)
	if readiness.IsZero() {
		ev.ProvisionSeconds = telemetry.Seconds(now.Sub(t.run.Started))
	} else {
		ev.ProvisionSeconds = telemetry.Seconds(readiness.Sub(t.run.Started))
		ev.ReadySeconds = telemetry.Seconds(now.Sub(readiness))
	}
	switch {
	case err == nil:
		ev.Outcome = "ready"
	case errors.Is(err, ErrInterrupted) || errors.Is(err, context.Canceled):
		ev.Outcome, ev.ErrorClass = "cancelled", telemetry.ErrInterrupted
	default:
		ev.Outcome, ev.ErrorClass = "failed", ErrorClass(err, !readiness.IsZero())
	}
	telemetry.Record(ev)
	if err != nil {
		telemetry.EndRun(t.cluster)
	}
}

// ErrorClass maps a failure to its class. The error's text is read here,
// on this computer, and never sent.
func ErrorClass(err error, readiness bool) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return telemetry.ErrTimeout
	}
	if errors.Is(err, cluster.ErrEngineNotInstalled) {
		return telemetry.ErrEngineUnreachable
	}
	text := strings.ToLower(err.Error())
	for _, c := range []struct {
		class string
		words []string
	}{
		{telemetry.ErrEngineUnreachable, []string{"engine unreachable", "cannot connect to the docker daemon", "docker daemon", "podman machine", "is the docker daemon running", "container engine"}},
		{telemetry.ErrInsufficientResources, []string{"not enough memory", "insufficient", "out of memory", "no space left", "disk space", "not enough cpu"}},
		{telemetry.ErrImagePull, []string{"pull", "manifest unknown", "image not found", "errimagepull", "imagepullbackoff"}},
		{telemetry.ErrPortConflict, []string{"address already in use", "port is already allocated", "port conflict"}},
		{telemetry.ErrTimeout, []string{"timed out", "timeout", "deadline exceeded"}},
		{telemetry.ErrNetwork, []string{"no such host", "connection refused", "network is unreachable", "tls handshake"}},
	} {
		for _, w := range c.words {
			if strings.Contains(text, w) {
				return c.class
			}
		}
	}
	if readiness {
		return telemetry.ErrReadiness
	}
	return telemetry.ErrProvision
}

var runtimeVersion = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// containerRuntime is the container engine kind uses and its major
// version (nil when it cannot be read within a second).
func containerRuntime() (string, *int) {
	engine, err := cluster.DetectContainerEngine()
	if err != nil {
		return "", nil
	}
	name := "other"
	switch engine.Name {
	case "docker", "podman":
		name = engine.Name
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, engine.Path, "--version").Output()
	if err != nil {
		return name, nil
	}
	m := runtimeVersion.FindStringSubmatch(string(out))
	if m == nil {
		return name, nil
	}
	major, err := strconv.Atoi(m[1])
	if err != nil || major > 999 {
		return name, nil
	}
	return name, &major
}

// recordSubmit records a submit of cluster's run: its attempt number, pass
// or fail and the check counts.
func recordSubmit(clusterName string, passed bool, total, ok int) {
	run := telemetry.LoadRun(clusterName)
	if run == nil {
		return
	}
	run.Attempts++
	_ = telemetry.SaveRun(clusterName, run)
	ev := run.Event(telemetry.Submit)
	ev.Attempt = min(run.Attempts, 1000)
	ev.Outcome = "fail"
	if passed {
		ev.Outcome = "pass"
	}
	ev.ChecksTotal, ev.ChecksPassed = telemetry.Int(min(total, 1000)), telemetry.Int(min(ok, total, 1000))
	if total == 0 {
		ev.ChecksTotal, ev.ChecksPassed = nil, nil
	}
	telemetry.Record(ev)
}

// teardownTelemetry starts timing the teardown of cluster's run; the func it
// returns records teardown (ok or failed) and, for a lab never submitted,
// run_abandoned, then forgets the run once the lab is gone.
func teardownTelemetry(clusterName string) func(ok bool) {
	run := telemetry.LoadRun(clusterName)
	if run == nil {
		return func(bool) {}
	}
	began := time.Now()
	return func(ok bool) {
		now := time.Now()
		ev := run.Event(telemetry.Teardown)
		ev.Outcome = "failed"
		if ok {
			ev.Outcome = "ok"
		}
		ev.TeardownSeconds = telemetry.Seconds(now.Sub(began))
		ev.SessionSeconds = telemetry.Seconds(began.Sub(run.Started))
		telemetry.Record(ev)
		if !ok {
			return
		}
		if run.Kind == "lab" && run.Attempts == 0 {
			abandoned := run.Event(telemetry.RunAbandoned)
			abandoned.SessionSeconds = ev.SessionSeconds
			telemetry.Record(abandoned)
		}
		telemetry.EndRun(clusterName)
	}
}

// RecordTimerStarted records the browser workspace's Start for cluster's run.
func RecordTimerStarted(clusterName string) {
	if run := telemetry.LoadRun(clusterName); run != nil {
		telemetry.Record(run.Event(telemetry.TimerStarted))
	}
}

// FlushTelemetry sends the waiting usage events: signed-in ones with the
// account of the site (as a guest when that sign-in is gone), guest ones
// without. It returns how many are left.
func FlushTelemetry(ctx context.Context) (int, error) {
	if telemetry.Pending() == 0 {
		return 0, nil
	}
	client, store, site, err := AccountDeps()
	if err != nil {
		return telemetry.Pending(), err
	}
	var creds *account.Credentials
	looked := false
	left, err := telemetry.Flush(ctx, func(ctx context.Context, signed bool, installID string, events []telemetry.Event) error {
		ctx, cancel := context.WithTimeout(ctx, SignalTimeout)
		defer cancel()
		if signed && !looked {
			looked = true
			if cr, err := client.Active(ctx, store, site); err == nil {
				creds = cr
			}
		}
		if !signed || creds == nil {
			return client.PostCLIEvents(ctx, store, nil, site, installID, events)
		}
		err := client.PostCLIEvents(ctx, store, creds, site, installID, events)
		if errors.Is(err, account.ErrSignedOut) || account.IsProofError(err) {
			creds = nil
			return client.PostCLIEvents(ctx, store, nil, site, installID, events)
		}
		return err
	}, account.Permanent)
	telemetry.NoteFailure(err != nil)
	return left, err
}

// SendTelemetry is FlushTelemetry at the end of a command: at most budget,
// and not at all when a send failed in the last 15 minutes (the background
// sender retries then).
func SendTelemetry(budget time.Duration) {
	if telemetry.Pending() == 0 || telemetry.RecentFailure(15*time.Minute) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	_, _ = FlushTelemetry(ctx)
}
