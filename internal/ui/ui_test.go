package ui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestReporter builds a Reporter writing status to screen and its log
// under a temp dir, so tests never touch ~/.astrona/logs or a real TTY.
func newTestReporter(t *testing.T, verbose bool, screen *bytes.Buffer) *Reporter {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "run.log")
	r, err := New("run", "demo", Options{Verbose: verbose, Screen: screen, LogPath: logPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func readLog(t *testing.T, r *Reporter) string {
	t.Helper()
	b, err := os.ReadFile(r.LogPath())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	return string(b)
}

func TestQuietModeCapturesOutputAndHidesItOnSuccess(t *testing.T) {
	var screen bytes.Buffer
	r := newTestReporter(t, false, &screen)

	task := r.Step("build the thing")
	fmt.Fprintln(task.Output(), "compiling module foo")
	task.Done()
	r.Close()

	if strings.Contains(screen.String(), "compiling module foo") {
		t.Errorf("quiet mode leaked subprocess output to screen:\n%s", screen.String())
	}
	if !strings.Contains(readLog(t, r), "compiling module foo") {
		t.Errorf("subprocess output missing from log:\n%s", readLog(t, r))
	}
}

func TestQuietModeFailPrintsCapturedOutput(t *testing.T) {
	var screen bytes.Buffer
	r := newTestReporter(t, false, &screen)

	task := r.Step("do a risky thing")
	fmt.Fprintln(task.Output(), "boom: something broke")
	err := task.Fail(fmt.Errorf("exit status 1"))
	r.Close()

	if err == nil || err.Error() != "exit status 1" {
		t.Fatalf("Fail must return the error unchanged, got %v", err)
	}
	if !strings.Contains(screen.String(), "boom: something broke") {
		t.Errorf("failed step must print its captured output:\n%s", screen.String())
	}
	if !strings.Contains(screen.String(), r.LogPath()) {
		t.Errorf("failed step must point at the full log:\n%s", screen.String())
	}
}

func TestVerboseModeStreamsOutputLive(t *testing.T) {
	var screen bytes.Buffer
	r := newTestReporter(t, true, &screen)
	// Route the "raw" passthrough into a buffer we can assert on instead of
	// the real os.Stdout.
	var raw bytes.Buffer
	r.raw = &raw

	task := r.Step("build the thing")
	fmt.Fprintln(task.Output(), "compiling module foo")
	task.Done()
	r.Close()

	if !strings.Contains(raw.String(), "compiling module foo") {
		t.Errorf("verbose mode must stream output live:\n%s", raw.String())
	}
	if !strings.Contains(readLog(t, r), "compiling module foo") {
		t.Errorf("verbose mode must still log output:\n%s", readLog(t, r))
	}
	if r.spin != nil {
		t.Errorf("verbose mode must not build a spinner")
	}
}

func TestInfoIsScreenSuppressedInQuietButAlwaysLogged(t *testing.T) {
	var screen bytes.Buffer
	r := newTestReporter(t, false, &screen)

	r.Info("using cached image abc123")
	r.Warn("kvm not available, falling back to tcg")
	r.Close()

	if strings.Contains(screen.String(), "using cached image") {
		t.Errorf("Info must not reach the screen in quiet mode:\n%s", screen.String())
	}
	if !strings.Contains(screen.String(), "kvm not available") {
		t.Errorf("Warn must reach the screen:\n%s", screen.String())
	}
	log := readLog(t, r)
	if !strings.Contains(log, "using cached image abc123") || !strings.Contains(log, "kvm not available") {
		t.Errorf("Info and Warn must both be logged:\n%s", log)
	}
}

func TestNonTTYOutputHasNoANSIEscapes(t *testing.T) {
	var screen bytes.Buffer
	r := newTestReporter(t, false, &screen)

	r.Section("Bootstrap")
	task := r.Step("first step")
	task.Done()
	r.Close()

	if strings.Contains(screen.String(), "\033[") {
		t.Errorf("non-TTY screen output must not contain ANSI escapes:\n%q", screen.String())
	}
}

func TestCloseResolvesADanglingStep(t *testing.T) {
	var screen bytes.Buffer
	r := newTestReporter(t, false, &screen)

	r.Step("started but never resolved")
	// No Done/Fail — Close must not hang or panic and must mark it failed.
	r.Close()

	if !strings.Contains(readLog(t, r), "FAIL: started but never resolved") {
		t.Errorf("Close must resolve a dangling step as failed:\n%s", readLog(t, r))
	}
}
