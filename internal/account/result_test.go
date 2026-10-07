package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (f *fakeSite) handleResult(w http.ResponseWriter, r *http.Request) {
	f.resultAuths = append(f.resultAuths, r.Header.Get("Authorization"))
	f.resultPaths = append(f.resultPaths, r.URL.EscapedPath())
	f.resultBody, _ = io.ReadAll(r.Body)
	status := http.StatusCreated
	if len(f.results) > 0 {
		status, f.results = f.results[0], f.results[1:]
	}
	w.WriteHeader(status)
	switch status {
	case http.StatusCreated:
		fmt.Fprintf(w, `{"id":"r1","attempt":3,"passed":true,"finished_at":"2026-10-07T12:00:00Z","url":%q}`, f.URL+"/labs/x")
	case http.StatusConflict:
		fmt.Fprint(w, `{"detail":"This attempt is already finished."}`)
	case http.StatusGone:
		if f.goneAsDetail {
			fmt.Fprint(w, `{"detail":"Session expired (labs service)."}`)
			return
		}
		fmt.Fprint(w, `{"statusCode":410,"statusMessage":"This lab session expired after 60 minutes — run the lab again to start a new attempt"}`)
	default:
		fmt.Fprintf(w, `{"statusMessage":"server says %d"}`, status)
	}
}

func resultSetup(t *testing.T, statuses ...int) (*fakeSite, Store, *Credentials) {
	t.Helper()
	f := newFakeSite(t)
	f.results = statuses
	s := Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	cr := testCreds(f, time.Now().Add(time.Hour))
	if err := s.Save(cr); err != nil {
		t.Fatal(err)
	}
	return f, s, cr
}

func TestSendLabResult(t *testing.T) {
	ctx := context.Background()
	f, s, cr := resultSetup(t)
	result := map[string]any{"lab": "astro-x", "pass": true, "earned": 3}
	lr, err := testClient(nil).SendLabResult(ctx, s, cr, "s 1", result)
	if err != nil {
		t.Fatal(err)
	}
	if lr.Attempt != 3 || !lr.Passed || lr.URL != f.URL+"/labs/x" {
		t.Errorf("LabResult = %+v", lr)
	}
	if f.resultPaths[0] != "/api/cli/lab-sessions/s%201/results" {
		t.Errorf("path = %s", f.resultPaths[0])
	}
	if f.resultAuths[0] != "Bearer access-0" {
		t.Errorf("auth = %q", f.resultAuths[0])
	}
	var body struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(f.resultBody, &body); err != nil || body.Result["lab"] != "astro-x" || body.Result["earned"] != 3.0 {
		t.Errorf("body = %s (%v)", f.resultBody, err)
	}
}

func TestSendLabResultErrors(t *testing.T) {
	ctx := context.Background()
	send := func(statuses ...int) (*fakeSite, error) {
		f, s, cr := resultSetup(t, statuses...)
		_, err := testClient(nil).SendLabResult(ctx, s, cr, "s1", map[string]any{})
		return f, err
	}
	if _, err := send(http.StatusNotFound); !errors.Is(err, ErrSessionGone) {
		t.Errorf("404: %v", err)
	}
	if _, err := send(http.StatusConflict); !errors.Is(err, ErrAttemptFinished) || !strings.Contains(err.Error(), "This attempt is already finished.") {
		t.Errorf("409: %v", err)
	}
	if _, err := send(http.StatusGone); !errors.Is(err, ErrSessionExpired) || !strings.Contains(err.Error(), "expired after 60 minutes") {
		t.Errorf("410 (website shape): %v", err)
	}
	fg, sg, crg := resultSetup(t, http.StatusGone)
	fg.goneAsDetail = true
	if _, err := testClient(nil).SendLabResult(ctx, sg, crg, "s1", nil); !errors.Is(err, ErrSessionExpired) || !strings.Contains(err.Error(), "Session expired (labs service).") {
		t.Errorf("410 (detail shape): %v", err)
	}
	if _, err := send(http.StatusBadGateway); err == nil || errors.Is(err, ErrSessionGone) || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("502: %v", err)
	}
	// 401: the sign-in is renewed once and the result sent again.
	f, err := send(http.StatusUnauthorized)
	if err != nil || f.refreshCalls != 1 || len(f.resultAuths) != 2 || f.resultAuths[1] != "Bearer access-r1" {
		t.Errorf("401 then renewed: %v, refreshes %d, auths %v", err, f.refreshCalls, f.resultAuths)
	}

	// A session id that would change the path is refused before sending.
	f2, s, cr := resultSetup(t)
	for _, bad := range []string{"", "../x", "a?b", "a#b"} {
		if _, err := testClient(nil).SendLabResult(ctx, s, cr, bad, nil); err == nil {
			t.Errorf("session id %q accepted", bad)
		}
	}
	if len(f2.resultPaths) != 0 {
		t.Errorf("sent anyway: %v", f2.resultPaths)
	}
}
