package labops

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/ui"
)

func TestIsPlaygroundName(t *testing.T) {
	for name, want := range map[string]bool{
		"ATS014/section-010/module-01/playground":            true,
		"astrona-io/ATS014/section-010/module-01/playground": true,
		"ATS014/section-010/module-01/lab-01":                false,
		"playground":                                         false,
	} {
		if IsPlaygroundName(name) != want {
			t.Errorf("IsPlaygroundName(%q) = %v", name, !want)
		}
	}
}

// The watchdog leaves quietly when there is no playground to watch, or when the
// playground it started for was destroyed or replaced before the deadline.
func TestWatchPlaygroundLeavesWhenThePlaygroundIsGone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := WatchPlayground(ctx, io.Discard, io.Discard, "astro-none", time.Millisecond, nil); err != nil {
		t.Fatalf("no state: %v", err)
	}

	sess := &labstate.Session{Site: "http://x", ID: "s-1", Kind: "playground", DeadlineAt: time.Now().Add(time.Hour).Format(time.RFC3339)}
	if err := labstate.Save("astro-play", &labstate.State{Session: sess}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- WatchPlayground(ctx, io.Discard, io.Discard, "astro-play", 10*time.Millisecond, nil) }()
	time.Sleep(50 * time.Millisecond)
	// A new run replaces the session: the old watchdog has nothing left to do.
	replaced := *sess
	replaced.ID = "s-2"
	if err := labstate.Save("astro-play", &labstate.State{Session: &replaced}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("replaced: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("the watchdog kept waiting for a replaced playground")
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
		if got := ClampDeadline(c.raw, now, c.minutes); got != c.want {
			t.Errorf("ClampDeadline(%s, %d) = %s, want %s", c.raw, c.minutes, got, c.want)
		}
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
		if IsWatchdogFor(cmdline, "astro-play") != want {
			t.Errorf("IsWatchdogFor(%q) = %v", cmdline, !want)
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
	prev := WatchdogCommandLine
	WatchdogCommandLine = func(int) (string, error) { mu.Lock(); defer mu.Unlock(); return cmdline, nil }
	t.Cleanup(func() { WatchdogCommandLine = prev })
	alive := func() bool {
		select {
		case <-exited:
			return false
		case <-time.After(100 * time.Millisecond):
			return true
		}
	}

	StopWatchdog(cmd.Process.Pid, "astro-play")
	if !alive() {
		t.Fatal("killed a process that isn't a watchdog")
	}
	setCmdline("astrona playground-watchdog astro-other")
	StopWatchdog(cmd.Process.Pid, "astro-play")
	if !alive() {
		t.Fatal("killed another playground's watchdog")
	}
	setCmdline("astrona playground-watchdog astro-play")
	StopWatchdog(cmd.Process.Pid, "astro-play")
	if alive() {
		t.Error("the playground's own watchdog was not stopped")
	}
}

func TestRunningPlaygroundsMatchesByClusterOrCatalogName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	prev := PlaygroundExists
	PlaygroundExists = func(string) bool { return true }
	t.Cleanup(func() { PlaygroundExists = prev })
	play := &labstate.Session{ID: "s-1", Kind: "playground", Lab: "ATS014/section-010/module-01/playground"}
	lab := &labstate.Session{ID: "s-2", Lab: "ATS014/section-010/module-01/lab-01"}
	if err := labstate.Save("astro-play", &labstate.State{Session: play}); err != nil {
		t.Fatal(err)
	}
	if err := labstate.Save("astro-lab", &labstate.State{Session: lab}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]int{
		"":           1, // every playground, never a lab
		"astro-play": 1,
		"play":       1,
		"astrona-io/ATS014/section-010/module-01/playground": 1,
		"ATS014/section-010/module-01/playground":            1,
		"astro-lab": 0,
		"ATS014/section-010/module-02/playground": 0,
	} {
		got, err := RunningPlaygrounds(name, func(string) {})
		if err != nil || len(got) != want {
			t.Errorf("runningPlaygrounds(%q) = %v, %v; want %d", name, got, err, want)
		}
	}
}

func TestExpiredWatchdogRechecksRunAfterMaintenanceWait(t *testing.T) {
	for _, change := range []string{"session", "deadline", "run", "local-only", "removed", "cancelled"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("PATH", t.TempDir())
			name := "astro-play"
			st := &labstate.State{StartedAt: "original", Session: &labstate.Session{
				ID: "old", Kind: "playground", DeadlineAt: time.Now().Add(-time.Minute).Format(time.RFC3339),
			}}
			if err := labstate.Save(name, st); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			called := false
			err := WatchPlayground(ctx, io.Discard, io.Discard, name, time.Millisecond,
				func(ctx context.Context, lab string, expected PlaygroundDeadline) error {
					called = true
					if expected.RunID != RunID(st) || expected.SessionID != "old" || lab != name {
						t.Fatal("watchdog lost the original run identity")
					}
					switch change {
					case "session":
						st.Session.ID = "renewed"
					case "deadline":
						st.Session.DeadlineAt = time.Now().Add(time.Hour).Format(time.RFC3339)
					case "run":
						st.StartedAt = "replacement"
					case "local-only":
						st.LocalOnly = true
					case "cancelled":
						cancel()
					}
					if change == "removed" {
						path, err := labstate.Path(name)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
					} else if err := labstate.Save(name, st); err != nil {
						t.Fatal(err)
					}
					// With no engine on PATH, any attempted deletion would return an error.
					return ExpirePlayground(ctx, lab, expected, ui.Discard(), DestroyOptions{Out: io.Discard, ForgetWarn: io.Discard})
				})
			if err != nil || !called {
				t.Fatalf("expire: called=%v, err=%v", called, err)
			}
			got, err := labstate.Load(name)
			if err != nil {
				t.Fatal(err)
			}
			if change != "removed" && (got == nil || RunID(got) != RunID(st)) {
				t.Fatal("new run was forgotten")
			}
		})
	}
}

func TestPlaygroundExpiryAndRenewalRespectMutationLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	name := "astro-play"
	st := &labstate.State{Session: &labstate.Session{ID: "old", Kind: "playground", DeadlineAt: time.Now().Add(-time.Minute).Format(time.RFC3339)}}
	if err := labstate.Save(name, st); err != nil {
		t.Fatal(err)
	}
	release, err := labstate.LockMutation(name)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	expected := PlaygroundDeadline{RunID: RunID(st), SessionID: st.Session.ID, DeadlineAt: st.Session.DeadlineAt}
	opts := DestroyOptions{Out: io.Discard, ForgetWarn: io.Discard}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := ExpirePlayground(ctx, name, expected, ui.Discard(), opts); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expiry ignored mutation lock: %v", err)
	}
	if _, err := RenewPlayground(context.Background(), io.Discard, name); err == nil || !strings.Contains(err.Error(), "being changed") {
		t.Fatalf("renewal ignored mutation lock: %v", err)
	}
	// A renewal can change the deadline while expiry waits on its lock.
	ctx, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWait()
	done := make(chan error, 1)
	go func() { done <- ExpirePlayground(ctx, name, expected, ui.Discard(), opts) }()
	select {
	case err := <-done:
		t.Fatalf("expiry did not wait for the mutation: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	st.Session.DeadlineAt = time.Now().Add(time.Hour).Format(time.RFC3339)
	if err := labstate.Save(name, st); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("expiry did not preserve the renewed run: %v", err)
	}
}
