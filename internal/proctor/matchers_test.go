package proctor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/config"
	"astrona/internal/executor"
	"astrona/internal/runtime"
)

func intp(n int) *int { return &n }

func TestMatchOutput(t *testing.T) {
	cases := []struct {
		c    config.ValidationCheck
		out  string
		want bool
	}{
		{config.ValidationCheck{}, "anything", true},
		{config.ValidationCheck{Expect: "3"}, "3", true},
		{config.ValidationCheck{Expect: "3"}, "2", false},
		{config.ValidationCheck{Contains: "Running"}, "pod/x Running", true},
		{config.ValidationCheck{ExpectRegex: `^v1\.3[0-9]\.`}, "v1.37.0", true},
		{config.ValidationCheck{ExpectRegex: `^v2`}, "v1.37.0", false},
		{config.ValidationCheck{Contains: "a", ExpectRegex: "z$"}, "a..y", false}, // every matcher must hold
	}
	for _, c := range cases {
		if ok, why := matchOutput(c.c, c.out); ok != c.want || (!ok && why == "") {
			t.Errorf("%+v on %q = %v (%q), want %v", c.c, c.out, ok, why, c.want)
		}
	}
}

func TestBounds(t *testing.T) {
	if !withinBounds(3, intp(2), nil) || withinBounds(1, intp(2), nil) || !withinBounds(2, intp(2), intp(2)) || withinBounds(5, nil, intp(4)) {
		t.Error("withinBounds wrong")
	}
	for want, got := range map[string]string{
		"exactly 2":  describeBounds(intp(2), intp(2)),
		"1–3":        describeBounds(intp(1), intp(3)),
		"at least 2": describeBounds(intp(2), nil),
		"at most 4":  describeBounds(nil, intp(4)),
	} {
		if got != want {
			t.Errorf("describeBounds = %q, want %q", got, want)
		}
	}
	if countLines("a\n\nb\n") != 2 || countLines("") != 0 {
		t.Error("countLines wrong")
	}
}

func TestHTTPCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte("<h1>Welcome to nginx!</h1>"))
	}))
	defer srv.Close()

	cases := []struct {
		c    config.ValidationCheck
		want bool
		msg  string
	}{
		{config.ValidationCheck{URL: srv.URL}, true, "→ 200"},
		{config.ValidationCheck{URL: srv.URL, Contains: "nginx"}, true, ""},
		{config.ValidationCheck{URL: srv.URL, Contains: "apache"}, false, "doesn't match"},
		{config.ValidationCheck{URL: srv.URL + "/missing"}, false, "→ 404, want 200"},
		{config.ValidationCheck{URL: srv.URL + "/missing", ExpectStatus: 404}, true, ""},
		{config.ValidationCheck{URL: "http://127.0.0.1:1/"}, false, "failed"},
	}
	for _, c := range cases {
		ok, msg := httpCheck(c.c)
		if ok != c.want || !strings.Contains(msg, c.msg) {
			t.Errorf("%+v = %v %q", c.c, ok, msg)
		}
	}
}

// TestJSONPathAndCountChecks drives runChecks with a fake kubectl that
// answers `get … -o jsonpath=…` and `get … -o name`, and checks the
// arguments it was called with.
func TestJSONPathAndCountChecks(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := `#!/bin/sh
echo "$*" >> "` + log + `"
case "$*" in
  *jsonpath=*) printf 2 ;;
  *"-o name"*) printf 'pod/web-1\npod/web-2\n' ;;
esac
`
	os.WriteFile(filepath.Join(bin, "kubectl"), []byte(script), 0700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	p := NewProctor(t.TempDir(), &runtime.LabEnvironment{KubeContext: "kind-x", Executor: executor.LocalExecutor{}})
	results, err := p.runChecks([]config.ValidationCheck{
		{Name: "replicas", Type: "jsonpath", Resource: "deploy/web -n shop", JSONPath: "{.spec.replicas}", Expect: "2"},
		{Name: "replicas wrong", Type: "jsonpath", Resource: "deploy/web -n shop", JSONPath: "{.spec.replicas}", Expect: "3"},
		{Name: "pods", Type: "count", Resource: "pods -l app=web -n shop", Min: intp(2)},
		{Name: "too few", Type: "count", Resource: "pods -l app=web -n shop", Min: intp(3)},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{true, false, true, false}
	for i, r := range results {
		if r.Pass != want[i] {
			t.Errorf("%s: pass = %v (%s), want %v", r.Name, r.Pass, r.Message, want[i])
		}
	}
	if !strings.Contains(results[1].Message, `{.spec.replicas} is "2" — want exactly "3"`) {
		t.Errorf("jsonpath message = %q", results[1].Message)
	}
	if results[3].Message != "found 2, want at least 3" {
		t.Errorf("count message = %q", results[3].Message)
	}
	calls, _ := os.ReadFile(log)
	for _, want := range []string{"--context kind-x get deploy/web -n shop -o jsonpath={.spec.replicas}", "--context kind-x get pods -l app=web -n shop -o name"} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("kubectl never called with %q:\n%s", want, calls)
		}
	}
}

func TestValidateChecks(t *testing.T) {
	bad := []config.ValidationCheck{
		{Type: "jsonpath", JSONPath: "{.x}", Expect: "1"},
		{Type: "jsonpath", Resource: "deploy/x", JSONPath: ".spec", Expect: "1"},
		{Type: "jsonpath", Resource: "deploy/x", JSONPath: "{.x}"},
		{Type: "count", Resource: "pods"},
		{Type: "count", Resource: "pods", Min: intp(3), Max: intp(1)},
		{Type: "http", URL: "file:///etc/passwd"},
		{Type: "http", URL: "http://x", ExpectStatus: 999},
		{Type: "command", Command: "true", ExpectRegex: "("},
	}
	for i, c := range bad {
		if err := config.ValidateChecks(&config.LabConfig{Validation: config.ValidationConfig{Checks: []config.ValidationCheck{c}}}); err == nil {
			t.Errorf("bad[%d] %+v accepted", i, c)
		}
	}
	good := []config.ValidationCheck{
		{Type: "jsonpath", Resource: "deploy/x", JSONPath: "{.spec.replicas}", Contains: "1"},
		{Type: "count", Resource: "pods -l a=b", Max: intp(0)},
		{Type: "http", URL: "http://web.localtest.me:8080/"},
		{Type: "command", Command: "true"},
	}
	if err := config.ValidateChecks(&config.LabConfig{Validation: config.ValidationConfig{Checks: good}}); err != nil {
		t.Error(err)
	}
}
