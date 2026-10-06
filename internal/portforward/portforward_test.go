package portforward

import (
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"astrona/internal/config"
)

const testLab = "astro-demo"

func testForward() config.PortForward {
	return config.PortForward{Name: "web", Resource: "service/frontend", HostPort: 18080, TargetPort: 80, Scheme: "http"}
}

// isolateHome points os.UserHomeDir (and so BaseDir) at a temp dir.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// writeForwardState lays out a forward dir the way startOne does, minus
// the supervisor process.
func writeForwardState(t *testing.T, spec Spec, pid int, st *Status) string {
	t.Helper()
	dir, err := forwardDir(spec.Lab, spec.Forward.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(filepath.Join(dir, specFile), spec); err != nil {
		t.Fatal(err)
	}
	if pid != 0 {
		if err := os.WriteFile(filepath.Join(dir, pidFile), []byte(strconv.Itoa(pid)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if st != nil {
		if err := writeJSONAtomic(filepath.Join(dir, statusFile), st); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func testSpec() Spec {
	return Spec{Lab: testLab, KubeContext: "kind-" + testLab, Forward: testForward().Normalized(), StartedAt: time.Now()}
}

// deadPID returns the pid of a process that has already exited.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestKubectlArgs(t *testing.T) {
	got := KubectlArgs(testSpec())
	want := []string{
		"--context", "kind-astro-demo",
		"--namespace", "default",
		"port-forward",
		"--address", "127.0.0.1",
		"svc/frontend",
		"18080:80",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("KubectlArgs = %v\nwant %v", got, want)
	}
}

func TestLocalURL(t *testing.T) {
	if got := LocalURL(testForward()); got != "http://127.0.0.1:18080" {
		t.Errorf("http forward: got %q", got)
	}
	pf := testForward()
	pf.Scheme = ""
	if got := LocalURL(pf); got != "tcp://127.0.0.1:18080" {
		t.Errorf("default scheme: got %q", got)
	}
}

func TestClassifyStdout(t *testing.T) {
	cases := map[string]stdoutLine{
		"Forwarding from 127.0.0.1:8080 -> 80": lineForwarding,
		"Handling connection for 8080":         lineConnection,
		"something else":                       lineOther,
		"":                                     lineOther,
	}
	for line, want := range cases {
		if got := classifyStdout(line); got != want {
			t.Errorf("classifyStdout(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestNextBackoff(t *testing.T) {
	d := minBackoff
	for range 10 {
		d = nextBackoff(d)
	}
	if d != maxBackoff {
		t.Fatalf("backoff after 10 doublings = %s, want capped at %s", d, maxBackoff)
	}
	if got := nextBackoff(time.Second); got != 2*time.Second {
		t.Fatalf("nextBackoff(1s) = %s", got)
	}
}

func TestIsTransient(t *testing.T) {
	transient := []string{
		"error: unable to forward port because pod is not running. Current status=Pending",
		"error: lost connection to pod",
		`error: timed out waiting for the condition`,
	}
	for _, e := range transient {
		if !isTransient(e) {
			t.Errorf("isTransient(%q) = false, want true", e)
		}
	}
	permanent := []string{
		`Error from server (NotFound): services "nope" not found`,
		`Error from server (Forbidden): pods is forbidden`,
		"unable to listen on any of the requested ports",
	}
	for _, e := range permanent {
		if isTransient(e) {
			t.Errorf("isTransient(%q) = true, want false", e)
		}
	}
}

func TestTruncate(t *testing.T) {
	long := strings.Repeat("x", maxErrorLen+10)
	if got := truncate(long); len(got) > maxErrorLen+len("…") {
		t.Fatalf("truncate kept %d bytes", len(got))
	}
	if got := truncate("short"); got != "short" {
		t.Fatalf("truncate(short) = %q", got)
	}
}

func TestPathValidation(t *testing.T) {
	isolateHome(t)
	for _, lab := range []string{"", "../etc", "a/b", ".hidden", "x..y"} {
		if _, err := labDir(lab); err == nil {
			t.Errorf("labDir(%q) accepted an unsafe lab name", lab)
		}
	}
	for _, name := range []string{"", "..", "a/b", "Web"} {
		if _, err := forwardDir(testLab, name); err == nil {
			t.Errorf("forwardDir(%q) accepted an unsafe forward name", name)
		}
	}
}

func TestListAndLoadSpec(t *testing.T) {
	isolateHome(t)

	if fs, err := List(""); err != nil || len(fs) != 0 {
		t.Fatalf("List on empty state = %v, %v", fs, err)
	}

	spec := testSpec()
	writeForwardState(t, spec, 0, nil)

	db := spec
	db.Forward.Name = "db"
	db.Forward.HostPort = 15432
	writeForwardState(t, db, 0, &Status{State: StateError, Restarts: 4, LastError: "boom"})

	fs, err := List(testLab)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 2 || fs[0].Spec.Forward.Name != "db" || fs[1].Spec.Forward.Name != "web" {
		t.Fatalf("List = %+v, want db then web", fs)
	}
	if fs[0].Status.Restarts != 4 || fs[0].Status.LastError != "boom" {
		t.Errorf("db status not loaded: %+v", fs[0].Status)
	}
	if fs[1].Status.State != StateNotReady {
		t.Errorf("missing status.json should read as NotReady, got %q", fs[1].Status.State)
	}
	if Count(testLab) != 2 || Count("astro-other") != 0 {
		t.Errorf("Count mismatch")
	}

	t.Run("tampered spec is skipped", func(t *testing.T) {
		bad := spec
		bad.Forward.Name = "evil"
		bad.Forward.HostPort = 19999
		bad.KubeContext = "prod-cluster"
		writeForwardState(t, bad, 0, nil)

		fs, err := List(testLab)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range fs {
			if f.Spec.Forward.Name == "evil" {
				t.Fatal("spec with a foreign kube context was loaded")
			}
		}
	})
}

func TestEffective(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	openPort := l.Addr().(*net.TCPAddr).Port

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedPort := closed.Addr().(*net.TCPAddr).Port
	closed.Close()

	alive := os.Getpid()
	mk := func(pid, port int, state State) Forward {
		f := Forward{Spec: testSpec(), PID: pid, Status: Status{State: state}}
		f.Spec.Forward.HostPort = port
		return f
	}

	cases := []struct {
		name string
		f    Forward
		want State
	}{
		{"ready and listening", mk(alive, openPort, StateReady), StateReady},
		{"ready but port closed", mk(alive, closedPort, StateReady), StateNotReady},
		{"supervisor dead", mk(deadPID(t), openPort, StateReady), StateStopped},
		{"no pid recorded", mk(0, openPort, StateReady), StateStopped},
		{"error passes through", mk(alive, closedPort, StateError), StateError},
		{"empty state", mk(alive, closedPort, ""), StateNotReady},
	}
	for _, c := range cases {
		if got := c.f.Effective(); got != c.want {
			t.Errorf("%s: Effective = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestStopRemovesStateAndSparesForeignPID(t *testing.T) {
	isolateHome(t)

	// A recorded pid that isn't its own process group leader — a plain
	// child, sharing this test's group — must never be signalled.
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	writeForwardState(t, testSpec(), child.Process.Pid, nil)

	n, err := Stop(testLab)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("Stop signalled a pid that isn't a process group leader: %v", err)
	}
	if n != 1 {
		t.Fatalf("Stop reported %d forwards, want 1", n)
	}
	if Count(testLab) != 0 {
		t.Fatal("state still present after Stop")
	}
	if n, err := Stop(testLab); err != nil || n != 0 {
		t.Fatalf("second Stop = %d, %v; want no-op", n, err)
	}
}

func TestPortFree(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := portFree(l.Addr().(*net.TCPAddr).Port); err == nil {
		t.Fatal("portFree reported a bound port as free")
	}
}

// installFakeKubectl puts a shell-script kubectl first on PATH. script is
// the body run for `port-forward`; `config get-contexts` prints contexts.
func installFakeKubectl(t *testing.T, portForwardBody, contexts string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = get-contexts ]; then printf '%s\\n' " + shellQuote(contexts) + "; exit 0; fi\n" +
		"done\n" +
		portForwardBody + "\n"
	if err := os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func readStatus(t *testing.T, dir string) Status {
	t.Helper()
	var st Status
	_ = readJSON(filepath.Join(dir, statusFile), &st)
	return st
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSuperviseReadyThenStopped(t *testing.T) {
	isolateHome(t)
	installFakeKubectl(t, "echo 'Forwarding from 127.0.0.1:18080 -> 80'\nexec sleep 30", "kind-"+testLab)
	dir := writeForwardState(t, testSpec(), 0, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Supervise(ctx, testLab, "web", io.Discard) }()

	waitFor(t, "Ready", func() bool { return readStatus(t, dir).State == StateReady })
	if st := readStatus(t, dir); st.KubectlPID == 0 {
		t.Error("kubectl pid not recorded while Ready")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Supervise returned %v after cancel", err)
	}
	if st := readStatus(t, dir); st.State != StateStopped {
		t.Fatalf("state after cancel = %s, want Stopped", st.State)
	}
}

func TestSuperviseRecordsFailureAndRetries(t *testing.T) {
	isolateHome(t)
	installFakeKubectl(t, "echo 'error: services \"frontend\" not found' >&2\nexit 1", "kind-"+testLab)
	dir := writeForwardState(t, testSpec(), 0, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Supervise(ctx, testLab, "web", io.Discard) }()

	waitFor(t, "first restart", func() bool { return readStatus(t, dir).Restarts >= 1 })
	st := readStatus(t, dir)
	if st.State != StateNotReady {
		t.Errorf("state after one failure = %s, want NotReady", st.State)
	}
	if !strings.Contains(st.LastError, `services "frontend" not found`) {
		t.Errorf("lastError = %q, want kubectl's stderr", st.LastError)
	}

	cancel()
	<-done
}

func TestSuperviseGivesUpWhenClusterGone(t *testing.T) {
	isolateHome(t)
	installFakeKubectl(t, "echo 'error: context not found' >&2\nexit 1", "kind-some-other-lab")
	dir := writeForwardState(t, testSpec(), 0, nil)

	err := Supervise(context.Background(), testLab, "web", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no longer exists") {
		t.Fatalf("Supervise = %v, want cluster-gone error", err)
	}
	if st := readStatus(t, dir); st.State != StateStopped {
		t.Fatalf("state = %s, want Stopped", st.State)
	}
}

func TestPauseKeepsSpecs(t *testing.T) {
	isolateHome(t)
	writeForwardState(t, testSpec(), deadPID(t), &Status{State: StateReady})

	n, err := Pause(testLab)
	if err != nil || n != 1 {
		t.Fatalf("Pause = %d, %v", n, err)
	}
	fs, err := List(testLab)
	if err != nil || len(fs) != 1 {
		t.Fatalf("specs after Pause = %v, %v — must be kept for start", fs, err)
	}
	if fs[0].Effective() != StateStopped {
		t.Errorf("paused forward shows %s, want Stopped", fs[0].Effective())
	}
	if n, _ := Pause("astro-none"); n != 0 {
		t.Error("Pause on a lab without forwards should be a no-op")
	}
}

func TestLinkedClusterSpec(t *testing.T) {
	isolateHome(t)

	idp := testSpec()
	idp.Forward.Name = "login"
	idp.Forward.HostPort = 18443
	idp.Forward.Cluster = "idp"
	idp.Cluster = testLab + "-idp"
	idp.KubeContext = "kind-" + testLab + "-idp"
	writeForwardState(t, idp, 0, nil)

	// Points at another lab's cluster, or at a cluster that isn't the
	// named linked cluster: both must be refused.
	foreign := idp
	foreign.Forward.Name = "foreign"
	foreign.Forward.HostPort = 18444
	foreign.Cluster = "astro-other"
	foreign.KubeContext = "kind-astro-other"
	writeForwardState(t, foreign, 0, nil)

	mismatch := idp
	mismatch.Forward.Name = "mismatch"
	mismatch.Forward.HostPort = 18445
	mismatch.Cluster = testLab + "-db"
	mismatch.KubeContext = "kind-" + testLab + "-db"
	writeForwardState(t, mismatch, 0, nil)

	fs, err := List(testLab)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Spec.Forward.Name != "login" {
		t.Fatalf("List = %+v — only the valid linked-cluster forward must load", fs)
	}
	if got := KubectlArgs(fs[0].Spec); got[1] != "kind-"+testLab+"-idp" {
		t.Errorf("kubectl context = %s", got[1])
	}
}
