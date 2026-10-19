package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"astrona/internal/labstate"
)

// trackingSite is an Astrona site for the whole tracking chain: sign-in, the
// lab session `astrona run` asks for, and the result `astrona submit` posts.
// It records every request, so a test can say what reached the site and what
// did not.
type trackingSite struct {
	*httptest.Server
	mu       sync.Mutex
	requests []trackedRequest
}

type trackedRequest struct {
	method, path, authorization string
	body                        []byte
}

func newTrackingSite(t *testing.T) *trackingSite {
	t.Helper()
	site := &trackingSite{}
	site.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		site.mu.Lock()
		site.requests = append(site.requests, trackedRequest{r.Method, r.URL.Path, r.Header.Get("Authorization"), body})
		site.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/cli/config":
			fmt.Fprint(w, `{"device_endpoint":"/api/cli/device","token_endpoint":"/api/cli/token","revocation_endpoint":"/api/cli/revoke"}`)
		case r.URL.Path == "/api/cli/device":
			fmt.Fprintf(w, `{"device_code":"d","user_code":"WXYZ-1234","verification_uri":%q,"verification_uri_complete":%q,"expires_in":60,"interval":1}`,
				site.URL+"/cli/authorize", site.URL+"/cli/authorize?code=WXYZ-1234")
		case r.URL.Path == "/api/cli/token":
			fmt.Fprint(w, `{"access_token":"secret-access","token_type":"Bearer","expires_in":300,"refresh_token":"secret-refresh","username":"student1","device_id":"dev-1"}`)
		case r.URL.Path == "/api/cli/lab-sessions" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"s1","token":"t","lab":"ATS/x","username":"student1","expires_at":"2099-01-01T00:00:00Z","url":%q}`, site.URL+"/labs/ATS/x")
		case r.URL.Path == "/api/cli/lab-sessions/s1/results" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"r1","attempt":1,"passed":true,"finished_at":"2026-10-07T12:00:00Z","url":%q}`, site.URL+"/labs/ATS/x?attempt=1")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(site.Close)
	return site
}

// count is how many requests to path (any method) the site has seen.
func (s *trackingSite) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.requests {
		if r.path == path {
			n++
		}
	}
	return n
}

// last is the latest request to path.
func (s *trackingSite) last(path string) (trackedRequest, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.requests) - 1; i >= 0; i-- {
		if s.requests[i].path == path {
			return s.requests[i], true
		}
	}
	return trackedRequest{}, false
}

const (
	trackingSessions = "/api/cli/lab-sessions"
	trackingResults  = "/api/cli/lab-sessions/s1/results"
)

// runAndSubmit does what `astrona run` and `astrona submit` do about the
// account, in order: pick the account for the lab, start its session, remember
// the lab, then send the result. It returns what sendResult said, and the lab's
// saved state.
func runAndSubmit(t *testing.T, flags *rootFlags) (sent bool, said string, state *labstate.State) {
	t.Helper()
	ctx := context.Background()
	acct, err := accountForLab(ctx, flags)
	if err != nil {
		t.Fatal(err)
	}
	var start *labStart
	captureStdout(t, func() {
		start, err = startLabSession(ctx, acct, "run", nil, flags.catalogLab)
	})
	if err != nil {
		t.Fatal(err)
	}
	rememberLab("astro-x", flags, start.sess)
	var w bytes.Buffer
	captureStdout(t, func() {
		sent = sendResult(ctx, &w, "astro-x", rememberedSource(flags), submissionResult{Lab: "astro-x", Pass: true})
	})
	state, err = labstate.Load("astro-x")
	if err != nil || state == nil {
		t.Fatalf("lab state: %+v, %v", state, err)
	}
	return sent, w.String(), state
}

// A student who is signed in has the lab tracked, and the result goes to the
// site they signed in to. A student who is not, or who runs local only, never
// sends anything: the Astrona site does not even hear about the run.
func TestResultsAreSentOnlyWhenSignedInAndNotLocalOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		signedOut bool
		flags     rootFlags
		wantSent  bool
		wantLocal bool   // the saved run is marked local only
		wantSaid  string // part of what submit said about sending
	}{
		{name: "signed in", wantSent: true, wantSaid: "Sent to your lab page (attempt 1)"},
		{name: "signed out", signedOut: true, wantSaid: signInHint},
		{name: "signed in, --local-only", flags: rootFlags{localOnly: true}, wantLocal: true, wantSaid: "This run is local only. No result was sent to Astrona."},
		{name: "signed in, --headless", flags: rootFlags{headless: true}, wantSaid: "wasn't started with a lab session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			site := newTrackingSite(t)
			t.Setenv("HOME", t.TempDir())
			client, store := withAccount(t, site.URL)
			if !tc.signedOut {
				captureStdout(t, func() {
					if err := login(context.Background(), client, store, site.URL, testDevice); err != nil {
						t.Fatal(err)
					}
				})
			}
			flags := tc.flags
			flags.catalogLab = "ATS/x"

			sent, said, state := runAndSubmit(t, &flags)

			if sent != tc.wantSent {
				t.Fatalf("sent = %v, want %v (submit said %q)", sent, tc.wantSent, said)
			}
			if !bytes.Contains([]byte(said), []byte(tc.wantSaid)) {
				t.Errorf("submit said %q, want it to contain %q", said, tc.wantSaid)
			}
			if state.LocalOnly != tc.wantLocal {
				t.Errorf("saved run local only = %v, want %v", state.LocalOnly, tc.wantLocal)
			}
			sessions, results := site.count(trackingSessions), site.count(trackingResults)
			if tc.wantSent {
				// Tracked: one session was started on the login site, the run
				// remembers it there, and the result went to that same site.
				if sessions != 1 || results != 1 {
					t.Fatalf("session requests = %d, result requests = %d, want 1 and 1", sessions, results)
				}
				if state.Session == nil || state.Session.Site != site.URL || state.Session.ID != "s1" {
					t.Fatalf("saved session = %+v, want s1 on %s", state.Session, site.URL)
				}
				req, _ := site.last(trackingResults)
				if req.method != http.MethodPost || req.authorization != "Bearer secret-access" {
					t.Errorf("result request = %s with %q, want a POST signed in as the student", req.method, req.authorization)
				}
				var body map[string]any
				if err := json.Unmarshal(req.body, &body); err != nil || len(body) == 0 {
					t.Errorf("result body %q is not a JSON result: %v", req.body, err)
				}
				return
			}
			// Not tracked: no session, and nothing reached the site about it.
			if state.Session != nil {
				t.Errorf("saved session = %+v, want none", state.Session)
			}
			if sessions != 0 || results != 0 {
				t.Errorf("session requests = %d, result requests = %d, want none", sessions, results)
			}
		})
	}
}

// The student signs in to one site; a result is never sent to the site of a
// session remembered for another one, nor is the sign-in used there.
func TestResultIsNotSentToAnotherSitesSession(t *testing.T) {
	login1, other := newTrackingSite(t), newTrackingSite(t)
	t.Setenv("HOME", t.TempDir())
	client, store := withAccount(t, login1.URL)
	captureStdout(t, func() {
		if err := login(context.Background(), client, store, login1.URL, testDevice); err != nil {
			t.Fatal(err)
		}
	})
	err := labstate.Save("astro-x", &labstate.State{
		Source:  &labstate.Source{Catalog: "ATS/x"},
		Session: &labstate.Session{Site: other.URL, ID: "s1", Lab: "ATS/x", URL: other.URL + "/labs/ATS/x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	sent := sendResult(context.Background(), &w, "astro-x", &labstate.Source{Catalog: "ATS/x"}, submissionResult{Pass: true})
	if sent || login1.count(trackingResults) != 0 || other.count(trackingResults) != 0 {
		t.Fatalf("sent=%v, results to the login site %d, to the session's site %d; want nothing sent",
			sent, login1.count(trackingResults), other.count(trackingResults))
	}
}
