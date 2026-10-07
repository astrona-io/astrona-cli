package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"astrona/internal/config"
	"astrona/internal/labstate"
	"astrona/internal/runtime"
)

func TestPickRunningLab(t *testing.T) {
	states := map[string]*labstate.State{
		"astro-a": {Source: &labstate.Source{Catalog: "ATS014/section-010/module-01/lab-02"}},
		"astro-b": {Source: &labstate.Source{Config: "/home/me/labs/b"}},
		"astro-g": {Source: &labstate.Source{Git: "https://github.com/org/labs", Config: "net-01", GitRef: "v2"}},
	}
	load := func(name string) (*labstate.State, error) { return states[name], nil }

	// Nothing running: no lab, no error — the usual "no lab here" follows.
	if name, src, err := pickRunningLab(nil, load); name != "" || src != nil || err != nil {
		t.Errorf("none running: %q, %v, %v", name, src, err)
	}
	// Exactly one: that one, through what run remembered.
	name, src, err := pickRunningLab([]string{"astro-b"}, load)
	if err != nil || name != "astro-b" || src.Config != "/home/me/labs/b" {
		t.Errorf("one running: %q, %+v, %v", name, src, err)
	}
	// One, but started by an older astrona: say how to name it.
	if _, _, err := pickRunningLab([]string{"astro-old"}, load); err == nil || !strings.Contains(err.Error(), "astrona submit -c <lab-dir>") {
		t.Errorf("unknown config: %v", err)
	}
	// Several: list them with how to name each.
	_, _, err = pickRunningLab([]string{"astro-a", "astro-b", "astro-g", "astro-old"}, load)
	if err == nil {
		t.Fatal("several running: no error")
	}
	for _, want := range []string{
		"several labs are running",
		"astro-a    astrona submit ATS014/section-010/module-01/lab-02",
		"astro-b    astrona submit -c /home/me/labs/b",
		"astro-g    astrona submit --git https://github.com/org/labs -c net-01 --git-ref v2",
		"astro-old    (started by an older astrona",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("several running: %q missing from\n%s", want, err)
		}
	}
}

func TestLabOfVM(t *testing.T) {
	known := []string{"astro-net", "astro-net-lab"}
	for vm, want := range map[string]string{
		"astro-net":            "astro-net",
		"astro-net-router":     "astro-net",
		"astro-net-lab-client": "astro-net-lab",
		"astro-other":          "astro-other",
	} {
		if got := labOfVM(vm, known); got != want {
			t.Errorf("labOfVM(%q) = %q, want %q", vm, got, want)
		}
	}
}

func TestApplySourceRoundTrip(t *testing.T) {
	flags := &rootFlags{configPath: "/labs/x", fileName: "lab.yaml", catalogLab: "ATS/x", fromCurrent: true}
	src := rememberedSource(flags)
	got := &rootFlags{configPath: "."}
	applySource(got, src)
	if got.configPath != "/labs/x" || got.fileName != "lab.yaml" || got.catalogLab != "ATS/x" || got.fromCurrent {
		t.Errorf("applySource = %+v", got)
	}
	// The default file name isn't stored, and comes back as the default.
	src = rememberedSource(&rootFlags{configPath: "/labs/y", fileName: "config.yaml"})
	applySource(got, src)
	if src.File != "" || got.fileName != "config.yaml" {
		t.Errorf("default file: %+v / %q", src, got.fileName)
	}
}

func TestSubmitFollowUp(t *testing.T) {
	for _, c := range []struct {
		name                                        string
		pass, sent, keep, jsonOut, tty, keepCluster bool
		want                                        followUp
	}{
		{"fail never prompts", false, true, false, false, true, false, followNothing},
		{"pass not sent", true, false, false, false, true, false, followNothing},
		{"pass sent, terminal", true, true, false, false, true, false, followAsk},
		{"pass sent, --keep", true, true, true, false, true, false, followNothing},
		{"pass sent, no terminal", true, true, false, false, false, false, followHint},
		{"pass sent, -o json", true, true, false, true, true, false, followHint},
		{"pass sent, keepCluster lab", true, true, false, false, true, true, followNothing},
	} {
		if got := submitFollowUp(c.pass, c.sent, c.keep, c.jsonOut, c.tty, c.keepCluster); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestOfferDestroy(t *testing.T) {
	for _, c := range []struct {
		name    string
		ask     bool
		answer  string
		destroy bool
	}{
		{"Enter deletes", true, "\n", true},
		{"y deletes", true, "y\n", true},
		{"YES deletes", true, "YES\n", true},
		{"n keeps", true, "n\n", false},
		{"closed input keeps", true, "", false},
		{"no terminal keeps", false, "y\n", false},
	} {
		var prompt, out bytes.Buffer
		destroyed := false
		offerDestroy(strings.NewReader(c.answer), &prompt, &out, c.ask, "ATS/x", func() error { destroyed = true; return nil })
		if destroyed != c.destroy {
			t.Errorf("%s: destroyed = %v", c.name, destroyed)
		}
		if c.ask != strings.Contains(prompt.String(), "Delete the lab cluster now? [Y/n] ") {
			t.Errorf("%s: prompt = %q", c.name, prompt.String())
		}
		kept := strings.Contains(out.String(), "Kept. Remove it later with: astrona destroy ATS/x")
		if kept == c.destroy {
			t.Errorf("%s: output = %q", c.name, out.String())
		}
	}
	// A failed delete is only a warning.
	var out bytes.Buffer
	offerDestroy(strings.NewReader("\n"), &bytes.Buffer{}, &out, true, "astro-x", func() error { return errors.New("boom") })
}

// fakeResultSite is an Astrona site that signs in and answers the result
// endpoint with status.
func fakeResultSite(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/cli/config":
			fmt.Fprint(w, `{"device_endpoint":"/api/cli/device","token_endpoint":"/api/cli/token","revocation_endpoint":"/api/cli/revoke"}`)
		case r.URL.Path == "/api/cli/device":
			fmt.Fprintf(w, `{"device_code":"d","user_code":"WXYZ-1234","verification_uri":%q,"verification_uri_complete":%q,"expires_in":60,"interval":1}`,
				srv.URL+"/cli/authorize", srv.URL+"/cli/authorize?code=WXYZ-1234")
		case r.URL.Path == "/api/cli/token":
			fmt.Fprint(w, `{"access_token":"secret-access","token_type":"Bearer","expires_in":300,"refresh_token":"secret-refresh","username":"student1","device_id":"dev-1"}`)
		case r.URL.Path == "/api/cli/lab-sessions/s1/results" && r.Method == http.MethodPost:
			calls.Add(1)
			if r.Header.Get("Authorization") != "Bearer secret-access" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(status)
			switch status {
			case http.StatusCreated:
				fmt.Fprintf(w, `{"id":"r1","attempt":3,"passed":true,"finished_at":"2026-10-07T12:00:00Z","url":%q}`, srv.URL+"/labs/x?attempt=3")
			case http.StatusConflict:
				fmt.Fprint(w, `{"detail":"This lab attempt already has a result."}`)
			case http.StatusGone:
				fmt.Fprint(w, `{"statusCode":410,"statusMessage":"This lab session expired after 60 minutes — run the lab again to start a new attempt"}`)
			default:
				fmt.Fprint(w, `{"statusMessage":"down"}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// sendSetup signs in to srv (unless signedOut) and remembers a session on
// session's site for astro-x.
func sendSetup(t *testing.T, srv *httptest.Server, signedOut bool, sessionSite string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	client, store := withAccount(t, srv.URL)
	if !signedOut {
		captureStdout(t, func() {
			if err := login(context.Background(), client, store, srv.URL, testDevice); err != nil {
				t.Fatal(err)
			}
		})
	}
	if sessionSite != "" {
		if err := labstate.Save("astro-x", &labstate.State{Session: &labstate.Session{Site: sessionSite, ID: "s1", Lab: "ATS/x", URL: sessionSite + "/labs/x"}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSendResult(t *testing.T) {
	result := map[string]any{"lab": "astro-x", "pass": true}
	for _, c := range []struct {
		name      string
		status    int
		signedOut bool
		session   bool
		catalog   string
		wantSent  bool
		want      string
		wantCalls int32
	}{
		{"201", http.StatusCreated, false, true, "ATS/x", true, "Sent to your lab page (attempt 3): {site}/labs/x?attempt=3", 1},
		{"404", http.StatusNotFound, false, true, "ATS/x", false, "This lab's session has expired or isn't yours — the result was not sent. Run the lab again to track a new attempt.", 1},
		{"409", http.StatusConflict, false, true, "ATS/x", false, "The result was not sent: This lab attempt already has a result.", 1},
		{"410", http.StatusGone, false, true, "ATS/x", false, "The result was not sent: This lab session expired after 60 minutes — run the lab again to start a new attempt\nStart a new attempt with: astrona reset ATS/x   (or: astrona destroy ATS/x, then astrona run ATS/x)", 1},
		{"5xx", http.StatusBadGateway, false, true, "ATS/x", false, "", 1},
		{"session, signed out", http.StatusCreated, true, true, "ATS/x", false, signInHint, 0},
		{"catalog lab, no session, signed out", http.StatusCreated, true, false, "ATS/x", false, signInHint, 0},
		{"catalog lab, no session, signed in", http.StatusCreated, false, false, "ATS/x", false, "wasn't started with a lab session", 0},
		{"-c lab, no session", http.StatusCreated, false, false, "", false, "", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv, calls := fakeResultSite(t, c.status)
			site := ""
			if c.session {
				site = srv.URL
			}
			sendSetup(t, srv, c.signedOut, site)
			var w bytes.Buffer
			var sent bool
			// Everything goes to w: under -o json that is stderr, so
			// stdout must stay empty.
			stdout := captureStdout(t, func() {
				sent = sendResult(context.Background(), &w, "astro-x", c.catalog, result)
			})
			if sent != c.wantSent {
				t.Errorf("sent = %v", sent)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			want := strings.ReplaceAll(c.want, "{site}", srv.URL)
			if want == "" && w.Len() != 0 {
				t.Errorf("output = %q, want none", w.String())
			}
			if !strings.Contains(w.String(), want) {
				t.Errorf("output = %q, want %q", w.String(), want)
			}
			if calls.Load() != c.wantCalls {
				t.Errorf("result requests = %d, want %d", calls.Load(), c.wantCalls)
			}
		})
	}
}

func TestSendResultOnlyToTheSessionsSite(t *testing.T) {
	srv, calls := fakeResultSite(t, http.StatusCreated)
	sendSetup(t, srv, false, "https://elsewhere.example.com")
	var w bytes.Buffer
	if sendResult(context.Background(), &w, "astro-x", "ATS/x", map[string]any{}) {
		t.Error("sent to a site the session isn't on")
	}
	if calls.Load() != 0 || !strings.Contains(w.String(), "the result was not sent") {
		t.Errorf("calls %d, output %q", calls.Load(), w.String())
	}
}

// fakeKube is an in-memory kubeconfig for the cmd-level context tests.
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
	sw := switchToLab("astro-x", "kind-astro-x")
	if sw == nil || kube.current != "kind-astro-x" {
		t.Fatalf("switch: %+v, current %q", sw, kube.current)
	}
	env := &runtime.LabEnvironment{Type: runtime.RuntimeKind, KubeContext: "kind-astro-x", Kubeconfig: "/k"}
	out := captureStdout(t, func() { printConnectHints(env, &config.LabConfig{}, "astro-x", sw) })
	if !strings.Contains(out, `kubectl now points at this lab (kind-astro-x). Your previous context "prod" comes back with: astrona destroy astro-x`) {
		t.Errorf("hints = %q", out)
	}

	var w bytes.Buffer
	releaseLab(&w, "astro-x")
	if kube.current != "prod" || !strings.Contains(w.String(), `kubectl points at "prod" again`) {
		t.Errorf("release: current %q, output %q", kube.current, w.String())
	}
}

func TestReleaseLabLeavesAnotherContext(t *testing.T) {
	kube := withFakeKube(t, "prod", "kind-astro-x", "staging")
	if switchToLab("astro-x", "kind-astro-x") == nil {
		t.Fatal("not switched")
	}
	kube.current = "staging"
	var w bytes.Buffer
	releaseLab(&w, "astro-x")
	if kube.current != "staging" || !strings.Contains(w.String(), `current context is "staging" now, not the lab's — left it as it is`) {
		t.Errorf("current %q, output %q", kube.current, w.String())
	}
}

func TestReleaseLabPreviousGone(t *testing.T) {
	kube := withFakeKube(t, "old", "kind-astro-x")
	if switchToLab("astro-x", "kind-astro-x") == nil {
		t.Fatal("not switched")
	}
	delete(kube.contexts, "old")
	var w bytes.Buffer
	releaseLab(&w, "astro-x")
	if kube.current != "kind-astro-x" || !strings.Contains(w.String(), `Your previous kubectl context "old" no longer exists`) {
		t.Errorf("current %q, output %q", kube.current, w.String())
	}
}

func TestKeepContextHints(t *testing.T) {
	env := &runtime.LabEnvironment{Type: runtime.RuntimeKind, KubeContext: "kind-astro-x", Kubeconfig: "/k"}
	out := captureStdout(t, func() { printConnectHints(env, &config.LabConfig{}, "astro-x", nil) })
	if !strings.Contains(out, "your own kubectl current-context is unchanged") || !strings.Contains(out, "kubectl --context kind-astro-x") {
		t.Errorf("hints = %q", out)
	}
	sw := &labstate.Switched{PreviousUnset: true}
	out = captureStdout(t, func() { printConnectHints(env, &config.LabConfig{}, "astro-x", sw) })
	if !strings.Contains(out, "Your previous context (none) comes back with: astrona destroy astro-x") {
		t.Errorf("hints = %q", out)
	}
}

func TestRememberLabAndForget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sess := &labstate.Session{Site: "https://astrona.io", ID: "s1", Lab: "ATS/x"}
	rememberLab("astro-x", &rootFlags{configPath: "/labs/x", fileName: "config.yaml", catalogLab: "ATS/x"}, sess)
	st, err := labstate.Load("astro-x")
	if err != nil || st == nil || st.Session.ID != "s1" || st.Source.Catalog != "ATS/x" {
		t.Fatalf("remembered: %+v, %v", st, err)
	}
	// A run without a session (a -c lab) forgets the old one.
	rememberLab("astro-x", &rootFlags{configPath: "/labs/x"}, nil)
	if st, _ := labstate.Load("astro-x"); st.Session != nil {
		t.Errorf("stale session kept: %+v", st.Session)
	}
	forgetLab("astro-x")
	if st, _ := labstate.Load("astro-x"); st != nil {
		t.Errorf("after forget: %+v", st)
	}
}

func TestPrintTimeLimit(t *testing.T) {
	if out := captureStdout(t, func() { printTimeLimit(&labstate.Session{MaxMinutes: 60}) }); out != "You have 60 minutes once the lab page opens.\n" {
		t.Errorf("with a limit: %q", out)
	}
	for _, s := range []*labstate.Session{nil, {}} {
		if out := captureStdout(t, func() { printTimeLimit(s) }); out != "" {
			t.Errorf("without a limit: %q", out)
		}
	}
}

// An expired session (410) is never followed by the delete question: the
// result wasn't sent.
func TestExpiredSessionNeverPrompts(t *testing.T) {
	if submitFollowUp(true, false, false, false, true, false) != followNothing {
		t.Error("a pass that wasn't sent must not offer to delete the lab")
	}
}
