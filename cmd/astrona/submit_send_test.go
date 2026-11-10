package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/labstate"
	"astrona/internal/runtime"
)

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
		if err := labstate.Save("astro-x", &labstate.State{Source: &labstate.Source{Catalog: "ATS/x"}, Session: &labstate.Session{Site: sessionSite, ID: "s1", Lab: "ATS/x", URL: sessionSite + "/labs/x"}}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSendResult(t *testing.T) {
	result := labops.Submission{Lab: "astro-x", Pass: true}
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
		{"session, signed out", http.StatusCreated, true, true, "ATS/x", false, "", 0},
		{"catalog lab, no session, signed out", http.StatusCreated, true, false, "ATS/x", false, labops.SignInHint, 0},
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
				sent = sendResult(context.Background(), &w, "astro-x", &labstate.Source{Catalog: c.catalog}, result)
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
	if sendResult(context.Background(), &w, "astro-x", &labstate.Source{Catalog: "ATS/x"}, labops.Submission{}) {
		t.Error("sent to a site the session isn't on")
	}
	if calls.Load() != 0 || !strings.Contains(w.String(), "the result was not sent") {
		t.Errorf("calls %d, output %q", calls.Load(), w.String())
	}
}

// Another lab with the same metadata.name (astrona submit -c ./other) never
// reaches the catalog lab's page.
func TestSendResultOnlyForTheConfigItWasStartedFrom(t *testing.T) {
	srv, calls := fakeResultSite(t, http.StatusCreated)
	sendSetup(t, srv, false, srv.URL)
	for _, src := range []*labstate.Source{{Config: "/home/me/other"}, {Catalog: "ATS/y"}} {
		var w bytes.Buffer
		if sendResult(context.Background(), &w, "astro-x", src, labops.Submission{}) {
			t.Errorf("%+v: sent to the session of another config", src)
		}
		if !strings.Contains(w.String(), "the result was not sent") || !strings.Contains(w.String(), "astrona submit ATS/x") {
			t.Errorf("%+v: output %q", src, w.String())
		}
	}
	// The same catalog lab still sends.
	var w bytes.Buffer
	if !sendResult(context.Background(), &w, "astro-x", &labstate.Source{Catalog: "ATS/x"}, labops.Submission{}) {
		t.Errorf("same catalog lab: not sent (%q)", w.String())
	}
	if calls.Load() != 1 {
		t.Errorf("result requests = %d, want 1", calls.Load())
	}
}

func TestKeepContextHints(t *testing.T) {
	env := &runtime.LabEnvironment{Type: runtime.RuntimeKind, KubeContext: "kind-astro-x", Kubeconfig: "/k"}
	out := captureStdout(t, func() { printConnectHints(env, &config.LabConfig{}, "astro-x", &labstate.Switched{Previous: "prod"}) })
	if !strings.Contains(out, `kubectl now points at this lab (kind-astro-x). Your previous context "prod" comes back with: astrona destroy astro-x`) {
		t.Errorf("hints = %q", out)
	}
	out = captureStdout(t, func() { printConnectHints(env, &config.LabConfig{}, "astro-x", nil) })
	if !strings.Contains(out, "your own kubectl current-context is unchanged") || !strings.Contains(out, "kubectl --context kind-astro-x") {
		t.Errorf("hints = %q", out)
	}
	sw := &labstate.Switched{PreviousUnset: true}
	out = captureStdout(t, func() { printConnectHints(env, &config.LabConfig{}, "astro-x", sw) })
	if !strings.Contains(out, "Your previous context (none) comes back with: astrona destroy astro-x") {
		t.Errorf("hints = %q", out)
	}
}

func TestPrintTimeLimit(t *testing.T) {
	if out := captureStdout(t, func() { labops.WriteTimeLimit(os.Stdout, &labstate.Session{MaxMinutes: 60}) }); out != "You have 60 minutes once the lab page opens.\n" {
		t.Errorf("with a limit: %q", out)
	}
	for _, s := range []*labstate.Session{nil, {}} {
		if out := captureStdout(t, func() { labops.WriteTimeLimit(os.Stdout, s) }); out != "" {
			t.Errorf("without a limit: %q", out)
		}
	}
}
