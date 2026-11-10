package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"astrona/internal/labops"
	"astrona/internal/labstate"
)

func navigationServer(t *testing.T) *Server {
	t.Helper()
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		port: 47300, origins: newOrigins(47300), auth: newAuth(secret, time.Now),
		assets: fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Workspace</title>")}},
	}
}

func TestNavigationCode(t *testing.T) {
	s := navigationServer(t)
	now := time.Now()
	s.auth.now = func() time.Time { return now }
	initial, err := MintCode(s.auth.secret, now, DefaultCodeTTL)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.auth.exchange(initial)
	if err != nil {
		t.Fatal(err)
	}
	request := func(token, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:47300/api/v1/auth/code", nil)
		r.RemoteAddr = "127.0.0.1:12345"
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		s.guard(s.routes()).ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct {
		name, token, origin string
		status              int
	}{
		{"missing token", "", "", http.StatusUnauthorized},
		{"invalid token", "unknown", "", http.StatusUnauthorized},
		{"foreign origin", token, "https://foreign.example", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := request(tc.token, tc.origin).Code; got != tc.status {
				t.Fatalf("status = %d, want %d", got, tc.status)
			}
		})
	}
	mint := func() string {
		t.Helper()
		w := request(token, "http://127.0.0.1:47300")
		if w.Code != http.StatusOK {
			t.Fatalf("mint status = %d", w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("launch code response must not be cached")
		}
		var body struct{ Code string }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Code
	}
	code := mint()
	if _, err := s.auth.exchange(code); err != nil {
		t.Fatalf("exchange minted code: %v", err)
	}
	if _, err := s.auth.exchange(code); !errors.Is(err, ErrCodeUsed) {
		t.Fatalf("reused code error = %v", err)
	}
	expiring := mint()
	now = now.Add(DefaultCodeTTL + time.Second)
	if _, err := s.auth.exchange(expiring); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expired code error = %v", err)
	}
}

func TestNewDocumentIncludesNewRunFrameOrigins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	s := navigationServer(t)
	policy := func() string {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:47300/", nil)
		r.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		s.guard(s.routes()).ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("document status = %d", w.Code)
		}
		return w.Header().Get("Content-Security-Policy")
	}
	before := policy()
	st := &labstate.State{StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := labstate.Save("astro-demo", st); err != nil {
		t.Fatal(err)
	}
	rec := RunRecord{Lab: "astro-demo", RunID: RunDigest(labops.RunID(st)), Targets: []Target{{Name: "astro-demo", Kind: TargetKind}}, Tabs: []Tab{
		FocusTab("astro-demo"),
		{Name: "app", Kind: TabBrowser, URL: "http://127.0.0.1:8080/"},
		{Name: "docs", Kind: TabDocs, URL: "https://docs.example/guide"},
	}}
	if err := WriteRun(rec); err != nil {
		t.Fatal(err)
	}
	after := policy()
	for _, origin := range []string{"http://127.0.0.1:8080", "https://docs.example"} {
		if strings.Contains(before, origin) || !strings.Contains(after, origin) {
			t.Fatalf("new document must add %s: before %q, after %q", origin, before, after)
		}
	}
	if !strings.HasPrefix(after, ContentSecurityPolicy+"; frame-src ") {
		t.Fatalf("base security policy changed: %q", after)
	}
}
