package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"astrona/internal/account"
	"astrona/internal/config"
	"astrona/internal/labstate"
)

// playgroundSite is an Astrona site that signs in, starts lab sessions
// answering with kind and deadline, and records every stop.
type playgroundSite struct {
	srv            *httptest.Server
	mu             sync.Mutex
	kind, deadline string
	created        []map[string]any
	stops          []string // "<session> <reason>"
}

func newPlaygroundSite(t *testing.T) *playgroundSite {
	t.Helper()
	p := &playgroundSite{kind: "playground"}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p.mu.Lock()
		defer p.mu.Unlock()
		switch {
		case r.URL.Path == "/api/cli/config":
			fmt.Fprint(w, `{"device_endpoint":"/api/cli/device","token_endpoint":"/api/cli/token","revocation_endpoint":"/api/cli/revoke"}`)
		case r.URL.Path == "/api/cli/device":
			fmt.Fprintf(w, `{"device_code":"d","user_code":"WXYZ-1234","verification_uri":%q,"verification_uri_complete":%q,"expires_in":60,"interval":1}`,
				p.srv.URL+"/cli/authorize", p.srv.URL+"/cli/authorize?code=WXYZ-1234")
		case r.URL.Path == "/api/cli/token":
			fmt.Fprint(w, `{"access_token":"secret-access","token_type":"Bearer","expires_in":300,"refresh_token":"secret-refresh","username":"student1","device_id":"dev-1"}`)
		case r.URL.Path == "/api/cli/lab-sessions":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			p.created = append(p.created, body)
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"s-new","token":"t","lab":"L","url":%q,"max_minutes":60,"kind":%q,"deadline_at":%q}`, p.srv.URL+"/labs/L", p.kind, p.deadline)
		case strings.HasSuffix(r.URL.Path, "/stop"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/cli/lab-sessions/"), "/stop")
			p.stops = append(p.stops, id+" "+body["reason"])
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *playgroundSite) set(kind, deadline string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.kind, p.deadline = kind, deadline
}

func (p *playgroundSite) lastCreated() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.created[len(p.created)-1]
}

func (p *playgroundSite) stopped() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.stops...)
}

// signIn signs in to p in a fresh HOME and returns the account a catalog
// lab runs under.
func (p *playgroundSite) signIn(t *testing.T) *labAccount {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	client, store := withAccount(t, p.srv.URL)
	captureStdout(t, func() {
		if err := login(context.Background(), client, store, p.srv.URL, testDevice); err != nil {
			t.Fatal(err)
		}
	})
	acct, err := requireSignIn(context.Background(), &rootFlags{catalogLab: "L"}, "run")
	if err != nil {
		t.Fatal(err)
	}
	return acct
}

// withSpawnedWatchdogs records the watchdogs started instead of starting
// them.
func withSpawnedWatchdogs(t *testing.T) *[]string {
	t.Helper()
	var spawned []string
	prev := spawnWatchdog
	spawnWatchdog = func(cluster string) (int, error) { spawned = append(spawned, cluster); return 4242, nil }
	t.Cleanup(func() { spawnWatchdog = prev })
	return &spawned
}

// run and reset start a playground the same way: as a playground with the
// config's time limit, its watchdog started once it is ready — and its
// clock stopped again when the build fails.
func TestLabStartPlayground(t *testing.T) {
	site := newPlaygroundSite(t)
	site.set("playground", time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	acct := site.signIn(t)
	spawned := withSpawnedWatchdogs(t)
	cfg := &config.LabConfig{Metadata: config.MetadataConfig{Name: "play", TimeLimit: "90m"}}
	begin := func(command, lab string) *labStart {
		var start *labStart
		captureStdout(t, func() {
			var err error
			if start, err = startLabSession(context.Background(), acct, command, cfg, lab); err != nil {
				t.Fatal(err)
			}
		})
		return start
	}

	for _, command := range []string{"run", "reset"} {
		start := begin(command, "ATS/s/m/playground")
		if created := site.lastCreated(); created["kind"] != "playground" || created["time_limit_minutes"] != float64(90) {
			t.Errorf("%s: session request = %v, want a 90-minute playground", command, created)
		}
		captureStdout(t, func() { start.done("astro-play") })
		start.abandon() // deferred: a ready playground keeps its clock
	}
	if len(*spawned) != 2 || len(site.stopped()) != 0 {
		t.Errorf("ready: watchdogs %v, stops %v; want one watchdog per start and no stop", *spawned, site.stopped())
	}

	// The build failed: the clock is stopped, no watchdog.
	begin("run", "ATS/s/m/playground").abandon()
	if got := site.stopped(); len(got) != 1 || got[0] != "s-new destroyed" {
		t.Errorf("failed build: stops %v, want [s-new destroyed]", got)
	}

	// A lab's clock starts on its page: a failed build has nothing to stop.
	site.set("lab", "")
	begin("run", "ATS/s/m/lab-01").abandon()
	if len(site.stopped()) != 1 || len(*spawned) != 2 {
		t.Errorf("lab: stops %v, watchdogs %v", site.stopped(), *spawned)
	}
}

// The site can't turn a lab into a playground, nor give a playground a
// deadline outside what was asked for.
func TestStartSessionDistrustsKindAndDeadline(t *testing.T) {
	site := newPlaygroundSite(t)
	acct := site.signIn(t)
	start := func(opts ...account.SessionOptions) *labstate.Session {
		var sess *labstate.Session
		captureStdout(t, func() {
			var err error
			if _, sess, err = acct.startSession(context.Background(), "run", "L", opts...); err != nil {
				t.Fatal(err)
			}
		})
		return sess
	}
	site.set("playground", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	if sess := start(); sess.IsPlayground() || sess.DeadlineAt != "" {
		t.Errorf("lab asked for, site said playground: %+v", sess)
	}

	play := account.SessionOptions{Kind: "playground", TimeLimitMinutes: 30}
	earliest := time.Now().Add(config.MinTimeLimit).Truncate(time.Second)
	if d, err := time.Parse(time.RFC3339, start(play).DeadlineAt); err != nil || d.Before(earliest) {
		t.Errorf("past deadline = %v (%v), want at least 5 minutes ahead", d, err)
	}
	site.set("playground", time.Now().Add(30*24*time.Hour).UTC().Format(time.RFC3339))
	if d, err := time.Parse(time.RFC3339, start(play).DeadlineAt); err != nil || d.After(time.Now().Add(30*time.Minute)) {
		t.Errorf("far deadline = %v (%v), want at most the 30 minutes asked for", d, err)
	}
}

func TestClampDeadline(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	for _, c := range []struct {
		raw     string
		minutes int
		want    string
	}{
		{at(time.Hour), 90, at(time.Hour)},
		{at(-time.Hour), 90, at(config.MinTimeLimit)},
		{at(time.Minute), 90, at(config.MinTimeLimit)},
		{at(3 * time.Hour), 90, at(90 * time.Minute)},
		{at(48 * time.Hour), 0, at(config.MaxTimeLimit)},
		{"soon", 90, "soon"},
	} {
		if got := clampDeadline(c.raw, now, c.minutes); got != c.want {
			t.Errorf("clampDeadline(%s, %d) = %s, want %s", c.raw, c.minutes, got, c.want)
		}
	}
}

// Starting over (run --yes, reset) ends the old playground first: its clock
// stops, and its watchdog — which would otherwise remove the new lab at the
// old deadline — has nothing left to watch.
func TestEndOldSessionStopsTheOldPlayground(t *testing.T) {
	site := newPlaygroundSite(t)
	site.signIn(t)
	old := &labstate.Session{Site: site.srv.URL, ID: "s-old", Kind: "playground", DeadlineAt: time.Now().Add(time.Hour).Format(time.RFC3339)}
	src := &labstate.Source{Catalog: "ATS/s/m/playground"}
	kube := &labstate.KubeContext{Lab: "kind-astro-play", Previous: "mine"}
	if err := labstate.Save("astro-play", &labstate.State{Source: src, KubeContext: kube, Session: old}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- watchPlayground(ctx, "astro-play", 10*time.Millisecond) }()
	time.Sleep(30 * time.Millisecond)

	endOldSession("astro-play")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watchdog: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the old watchdog kept waiting for the replaced playground")
	}
	if got := site.stopped(); len(got) != 1 || got[0] != "s-old destroyed" {
		t.Errorf("stops = %v, want [s-old destroyed]", got)
	}
	st, err := labstate.Load("astro-play")
	if err != nil || st == nil || st.Session != nil || st.Source == nil || st.KubeContext == nil || st.KubeContext.Previous != "mine" {
		t.Errorf("state after = %+v, %v; want the session gone, the rest kept", st, err)
	}
}

func TestRunningPlaygroundsForgetsGoneLabs(t *testing.T) {
	site := newPlaygroundSite(t)
	site.signIn(t)
	prev := playgroundExists
	playgroundExists = func(name string) bool { return name == "astro-here" }
	t.Cleanup(func() { playgroundExists = prev })
	for _, name := range []string{"astro-here", "astro-gone"} {
		sess := &labstate.Session{Site: site.srv.URL, ID: name, Kind: "playground", Lab: "ATS/s/m/playground"}
		if err := labstate.Save(name, &labstate.State{Session: sess}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := runningPlaygrounds("")
	if err != nil || len(got) != 1 || got[0] != "astro-here" {
		t.Errorf("runningPlaygrounds = %v, %v; want [astro-here]", got, err)
	}
	if st, _ := labstate.Load("astro-gone"); st != nil {
		t.Errorf("a gone playground is still remembered: %+v", st)
	}
	if stops := site.stopped(); len(stops) != 1 || stops[0] != "astro-gone destroyed" {
		t.Errorf("stops = %v, want the gone playground's clock stopped", stops)
	}
}

func TestIsWatchdogFor(t *testing.T) {
	for cmdline, want := range map[string]bool{
		"/usr/local/bin/astrona playground-watchdog astro-play": true,
		"astrona playground-watchdog astro-play-2":              false,
		"astrona playground-watchdog":                           false,
		"sleep 30":                                              false,
		"vim astro-play":                                        false,
	} {
		if isWatchdogFor(cmdline, "astro-play") != want {
			t.Errorf("isWatchdogFor(%q) = %v", cmdline, !want)
		}
	}
}

// A remembered watchdog pid is only signalled while that process still
// runs this playground's watchdog — never a process that reused the pid.
func TestStopWatchdogChecksTheCommandLine(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Skipf("can't start sleep: %v", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	var mu sync.Mutex
	cmdline := "sleep 30"
	setCmdline := func(s string) { mu.Lock(); cmdline = s; mu.Unlock() }
	prev := watchdogCommandLine
	watchdogCommandLine = func(int) (string, error) { mu.Lock(); defer mu.Unlock(); return cmdline, nil }
	t.Cleanup(func() { watchdogCommandLine = prev })
	alive := func() bool {
		select {
		case <-exited:
			return false
		case <-time.After(100 * time.Millisecond):
			return true
		}
	}

	stopWatchdog(cmd.Process.Pid, "astro-play")
	if !alive() {
		t.Fatal("killed a process that isn't a watchdog")
	}
	setCmdline("astrona playground-watchdog astro-other")
	stopWatchdog(cmd.Process.Pid, "astro-play")
	if !alive() {
		t.Fatal("killed another playground's watchdog")
	}
	setCmdline("astrona playground-watchdog astro-play")
	stopWatchdog(cmd.Process.Pid, "astro-play")
	if alive() {
		t.Error("the playground's own watchdog was not stopped")
	}
}
