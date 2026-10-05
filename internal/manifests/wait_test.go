package manifests

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"astrona/internal/config"
	"astrona/internal/ui"
)

func TestWaitArgs(t *testing.T) {
	cases := []struct {
		w    config.WaitFor
		want []string
	}{
		{config.WaitFor{Resource: "deploy/web"},
			[]string{"--context", "kind-x", "--namespace", "default", "rollout", "status", "deploy/web", "--timeout=90s"}},
		{config.WaitFor{Resource: "deployment", Selector: "app=web", Namespace: "shop"},
			[]string{"--context", "kind-x", "--namespace", "shop", "rollout", "status", "deployment", "--selector=app=web", "--timeout=90s"}},
		{config.WaitFor{Resource: "pod", Selector: "app=web"},
			[]string{"--context", "kind-x", "--namespace", "default", "wait", "pod", "--selector=app=web", "--for=condition=Ready", "--timeout=90s"}},
		{config.WaitFor{Resource: "nodes", All: true},
			[]string{"--context", "kind-x", "--namespace", "default", "wait", "nodes", "--all", "--for=condition=Ready", "--timeout=90s"}},
		{config.WaitFor{Resource: "certificates.cert-manager.io/tls", Condition: "Ready"},
			[]string{"--context", "kind-x", "--namespace", "default", "wait", "certificates.cert-manager.io/tls", "--for=condition=Ready", "--timeout=90s"}},
	}
	for _, c := range cases {
		if got := WaitArgs("kind-x", c.w, 90*time.Second); !reflect.DeepEqual(got, c.want) {
			t.Errorf("WaitArgs(%+v)\n got %v\nwant %v", c.w, got, c.want)
		}
	}

	// Sub-second remainder still gives kubectl a usable timeout.
	args := WaitArgs("kind-x", config.WaitFor{Resource: "pod/x"}, 200*time.Millisecond)
	if args[len(args)-1] != "--timeout=1s" {
		t.Errorf("sub-second timeout arg = %s", args[len(args)-1])
	}
}

// installFakeKubectl puts a script named kubectl first on PATH. It counts
// `wait`/`rollout` attempts in a file and runs body with $n = attempt
// number; `get` calls (diagnostics) just print a marker.
func installFakeKubectl(t *testing.T, body string) (countFile string) {
	t.Helper()
	dir := t.TempDir()
	countFile = filepath.Join(dir, "count")
	script := `#!/bin/sh
for a in "$@"; do
  if [ "$a" = get ]; then echo "DIAG: $*"; exit 0; fi
done
n=$(( $(cat "` + countFile + `" 2>/dev/null || echo 0) + 1 ))
echo $n > "` + countFile + `"
` + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	old := waitPollInterval
	waitPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { waitPollInterval = old })
	return countFile
}

func attempts(t *testing.T, countFile string) int {
	t.Helper()
	data, _ := os.ReadFile(countFile)
	n, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	return n
}

func TestWaitForRetriesUntilResourceExists(t *testing.T) {
	count := installFakeKubectl(t, `if [ $n -lt 3 ]; then echo 'error: no matching resources found' >&2; exit 1; fi
echo 'pod/web-1 condition met'`)

	err := WaitFor([]config.WaitFor{{Resource: "pod", Selector: "app=web", Timeout: "10s"}}, "kind-x", ui.Discard())
	if err != nil {
		t.Fatalf("WaitFor: %v", err)
	}
	if n := attempts(t, count); n != 3 {
		t.Fatalf("attempts = %d, want 3 (two not-yet, one success)", n)
	}
}

func TestWaitForFailsFastOnRealError(t *testing.T) {
	count := installFakeKubectl(t, `echo 'error: timed out waiting for the condition on pods/web-1' >&2; exit 1`)

	err := WaitFor([]config.WaitFor{{Name: "web pods", Resource: "pod", Selector: "app=web", Timeout: "10s"}}, "kind-x", ui.Discard())
	if err == nil || !strings.Contains(err.Error(), "'web pods' not ready") || !strings.Contains(err.Error(), "timed out waiting for the condition") {
		t.Fatalf("error = %v", err)
	}
	if n := attempts(t, count); n != 1 {
		t.Fatalf("attempts = %d, want 1 — a real failure must not be retried", n)
	}
}

func TestWaitForTimesOutWhileNeverCreated(t *testing.T) {
	installFakeKubectl(t, `echo 'Error from server (NotFound): deployments.apps "web" not found' >&2; exit 1`)

	start := time.Now()
	err := WaitFor([]config.WaitFor{{Resource: "deploy/web", Timeout: "1s"}}, "kind-x", ui.Discard())
	if err == nil || !strings.Contains(err.Error(), "timed out after 1s") || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s, timeout not honored", elapsed)
	}
}

func TestWaitForStopsAtFirstFailingGate(t *testing.T) {
	count := installFakeKubectl(t, `exit 1`)

	err := WaitFor([]config.WaitFor{
		{Resource: "deploy/a", Timeout: "5s"},
		{Resource: "deploy/b", Timeout: "5s"},
	}, "kind-x", ui.Discard())
	if err == nil || !strings.Contains(err.Error(), "deploy/a") {
		t.Fatalf("error = %v, want failure on first gate", err)
	}
	if n := attempts(t, count); n != 1 {
		t.Fatalf("attempts = %d, second gate should never run", n)
	}
}

func TestIsNotYet(t *testing.T) {
	for _, s := range []string{
		"error: no matching resources found",
		`Error from server (NotFound): pods "x" not found`,
		`error: the server doesn't have a resource type "certificates"`,
	} {
		if !isNotYet(s) {
			t.Errorf("isNotYet(%q) = false", s)
		}
	}
	for _, s := range []string{"error: timed out waiting for the condition", "Error from server (Forbidden): x", ""} {
		if isNotYet(s) {
			t.Errorf("isNotYet(%q) = true", s)
		}
	}
}
