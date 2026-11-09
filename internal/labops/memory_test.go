package labops

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"astrona/internal/labstate"
)

// switchToLab is SwitchToLab for a test: nil when it couldn't.
func switchToLab(t *testing.T, clusterName, labContext string) *labstate.Switched {
	t.Helper()
	sw, err := SwitchToLab(clusterName, labContext)
	if err != nil {
		t.Logf("not switched: %s", err)
	}
	return sw
}

// rememberLab is RememberLab for a test, with the source as cmd remembers it.
func rememberLab(t *testing.T, clusterName string, src LabSource, sess *labstate.Session) {
	t.Helper()
	if err := RememberLab(clusterName, RememberedSource(src), false, sess); err != nil {
		t.Fatal(err)
	}
}

// fakeKube is an in-memory kubeconfig for the context tests.
type fakeKube struct {
	current  string
	contexts map[string]bool
}

func (f *fakeKube) Current() (string, bool, error) { return f.current, f.current != "", nil }
func (f *fakeKube) Use(n string) error {
	if !f.contexts[n] {
		return fmt.Errorf("no context %s", n)
	}
	f.current = n
	return nil
}
func (f *fakeKube) Unset() error                  { f.current = ""; return nil }
func (f *fakeKube) Exists(n string) (bool, error) { return f.contexts[n], nil }

func withFakeKube(t *testing.T, current string, contexts ...string) *fakeKube {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	f := &fakeKube{current: current, contexts: map[string]bool{current: current != ""}}
	for _, c := range contexts {
		f.contexts[c] = true
	}
	prev := kubeContexts
	kubeContexts = func() (labstate.Contexts, error) { return f, nil }
	t.Cleanup(func() { kubeContexts = prev })
	return f
}

func TestSwitchAndReleaseLab(t *testing.T) {
	kube := withFakeKube(t, "prod", "kind-astro-x")
	sw := switchToLab(t, "astro-x", "kind-astro-x")
	if sw == nil || kube.current != "kind-astro-x" {
		t.Fatalf("switch: %+v, current %q", sw, kube.current)
	}

	var w bytes.Buffer
	ReleaseLab(&w, "astro-x")
	if kube.current != "prod" || !strings.Contains(w.String(), `kubectl points at "prod" again`) {
		t.Errorf("release: current %q, output %q", kube.current, w.String())
	}
}

func TestReleaseLabLeavesAnotherContext(t *testing.T) {
	kube := withFakeKube(t, "prod", "kind-astro-x", "staging")
	if switchToLab(t, "astro-x", "kind-astro-x") == nil {
		t.Fatal("not switched")
	}
	kube.current = "staging"
	var w bytes.Buffer
	ReleaseLab(&w, "astro-x")
	if kube.current != "staging" || !strings.Contains(w.String(), `current context is "staging" now, not the lab's — left it as it is`) {
		t.Errorf("current %q, output %q", kube.current, w.String())
	}
}

func TestReleaseLabPreservesInheritedShellContext(t *testing.T) {
	kube := withFakeKube(t, "prod", "kind-astro-x")
	if switchToLab(t, "astro-x", "kind-astro-x") == nil {
		t.Fatal("not switched")
	}
	t.Setenv(LabShellEnvVar, "astro-x")
	var out bytes.Buffer
	ReleaseLab(&out, "astro-x")
	st, err := labstate.Load("astro-x")
	if err != nil || st == nil || st.KubeContext == nil || st.KubeContext.Previous != "prod" {
		t.Fatalf("original context was lost: %+v, %v", st, err)
	}
	if kube.current != "kind-astro-x" || out.Len() != 0 {
		t.Fatalf("inherited context changed: %s, %s", kube.current, out.String())
	}
}

func TestReleaseLabPreviousGone(t *testing.T) {
	kube := withFakeKube(t, "old", "kind-astro-x")
	if switchToLab(t, "astro-x", "kind-astro-x") == nil {
		t.Fatal("not switched")
	}
	delete(kube.contexts, "old")
	var w bytes.Buffer
	ReleaseLab(&w, "astro-x")
	if kube.current != "kind-astro-x" || !strings.Contains(w.String(), `Your previous kubectl context "old" no longer exists`) {
		t.Errorf("current %q, output %q", kube.current, w.String())
	}
}

func TestRememberLabAndForget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess := &labstate.Session{Site: "https://astrona.io", ID: "s1", Lab: "ATS/x"}
	rememberLab(t, "astro-x", LabSource{Config: "/labs/x", File: "config.yaml", Catalog: "ATS/x"}, sess)
	st, err := labstate.Load("astro-x")
	if err != nil || st == nil || st.Session.ID != "s1" || st.Source.Catalog != "ATS/x" {
		t.Fatalf("remembered: %+v, %v", st, err)
	}
	// A run without a session (a -c lab) forgets the old one.
	rememberLab(t, "astro-x", LabSource{Config: "/labs/x"}, nil)
	if st, _ := labstate.Load("astro-x"); st.Session != nil {
		t.Errorf("stale session kept: %+v", st.Session)
	}
	ForgetLab(io.Discard, "astro-x", ForgetOptions{})
	if st, _ := labstate.Load("astro-x"); st != nil {
		t.Errorf("after forget: %+v", st)
	}
}
