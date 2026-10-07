package account

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIdP is a site plus a Keycloak-like issuer on one httptest server.
type fakeIdP struct {
	*httptest.Server
	mu sync.Mutex
	// pollAnswers are the token endpoint's device-code answers, in order.
	pollAnswers []string
	// refresh answers refresh_token grants: "" = new tokens, else an OAuth error code.
	refresh      string
	refreshCalls int
	revoked      []string
	// sessions are the lab-session endpoint's statuses, in order (then 201).
	sessions     []int
	sessionAuths []string
	issuerName   string // what discovery claims; defaults to the real issuer
	endpointHost string // overrides the endpoints' host
}

func jwtWith(claims map[string]string) string {
	b, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(b) + ".sig"
}

func newFakeIdP(t *testing.T) *fakeIdP {
	f := &fakeIdP{}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeIdP) issuer() string { return f.URL + "/realms/astrona" }

func (f *fakeIdP) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	oauthErr := func(status int, code string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error":%q,"error_description":"desc for %s"}`, code, code)
	}
	tokens := func(access, refresh string) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": access, "refresh_token": refresh, "token_type": "Bearer", "expires_in": 300,
			"id_token": jwtWith(map[string]string{"preferred_username": "student1"}),
		})
	}
	switch r.URL.Path {
	case "/api/cli/config":
		fmt.Fprintf(w, `{"issuer":%q,"client_id":"astrona-cli"}`, f.issuer())
	case "/realms/astrona/.well-known/openid-configuration":
		iss, host := f.issuer(), f.URL
		if f.issuerName != "" {
			iss = f.issuerName
		}
		if f.endpointHost != "" {
			host = f.endpointHost
		}
		base := host + "/realms/astrona/protocol/openid-connect"
		json.NewEncoder(w).Encode(map[string]string{
			"issuer": iss, "device_authorization_endpoint": base + "/auth/device", "token_endpoint": base + "/token",
			"revocation_endpoint": base + "/revoke", "end_session_endpoint": base + "/logout",
		})
	case "/realms/astrona/protocol/openid-connect/auth/device":
		r.ParseForm()
		if r.Form.Get("client_id") != "astrona-cli" || r.Form.Get("scope") != Scope {
			oauthErr(400, "invalid_request")
			return
		}
		fmt.Fprintf(w, `{"device_code":"dev-123","user_code":"ABCD-EFGH","verification_uri":%q,"verification_uri_complete":%q,"expires_in":600,"interval":5}`,
			f.URL+"/device", f.URL+"/device?user_code=ABCD-EFGH")
	case "/realms/astrona/protocol/openid-connect/token":
		r.ParseForm()
		switch r.Form.Get("grant_type") {
		case deviceCodeGrant:
			if r.Form.Get("device_code") != "dev-123" {
				oauthErr(400, "invalid_grant")
				return
			}
			next := "ok"
			if len(f.pollAnswers) > 0 {
				next, f.pollAnswers = f.pollAnswers[0], f.pollAnswers[1:]
			}
			if next != "ok" {
				oauthErr(400, next)
				return
			}
			tokens("access-1", "refresh-1")
		case "refresh_token":
			f.refreshCalls++
			if f.refresh != "" {
				oauthErr(400, f.refresh)
				return
			}
			tokens(fmt.Sprintf("access-r%d", f.refreshCalls), fmt.Sprintf("refresh-r%d", f.refreshCalls))
		default:
			oauthErr(400, "unsupported_grant_type")
		}
	case "/realms/astrona/protocol/openid-connect/revoke":
		r.ParseForm()
		f.revoked = append(f.revoked, r.Form.Get("token"))
	case "/api/cli/lab-sessions":
		f.sessionAuths = append(f.sessionAuths, r.Header.Get("Authorization"))
		status := http.StatusCreated
		if len(f.sessions) > 0 {
			status, f.sessions = f.sessions[0], f.sessions[1:]
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusCreated {
			fmt.Fprintf(w, `{"statusCode":%d,"statusMessage":"server says %d"}`, status, status)
			return
		}
		var body struct{ Lab string }
		json.NewDecoder(r.Body).Decode(&body)
		fmt.Fprintf(w, `{"id":"s1","token":"tok","lab":%q,"username":"student1","expires_at":"2026-10-07T20:00:00","url":%q}`,
			body.Lab, f.URL+"/labs/"+body.Lab+"?t=tok")
	default:
		http.NotFound(w, r)
	}
}

// testClient records sleeps instead of sleeping.
func testClient(sleeps *[]time.Duration) *Client {
	c := NewClient()
	c.Sleep = func(ctx context.Context, d time.Duration) error {
		if sleeps != nil {
			*sleeps = append(*sleeps, d)
		}
		return ctx.Err()
	}
	return c
}

func TestResolveSite(t *testing.T) {
	env := func(v string) func(string) string { return func(string) string { return v } }
	for raw, want := range map[string]string{
		"":                           DefaultSite,
		"http://localhost:3000":      "http://localhost:3000",
		"http://127.0.0.1:3000/":     "http://127.0.0.1:3000",
		"https://Staging.Astrona.io": "https://staging.astrona.io",
	} {
		if got, err := ResolveSite(env(raw)); err != nil || got != want {
			t.Errorf("ResolveSite(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"http://astrona.io", "ftp://astrona.io", "https://u:p@astrona.io", "https://astrona.io/app", "astrona.io", "https://astrona.io?x=1"} {
		if got, err := ResolveSite(env(raw)); err == nil {
			t.Errorf("ResolveSite(%q) = %q, want an error", raw, got)
		}
	}
}

func TestConfigAndDiscovery(t *testing.T) {
	f := newFakeIdP(t)
	c := testClient(nil)
	ctx := context.Background()
	sc, err := c.FetchSiteConfig(ctx, f.URL)
	if err != nil || sc.Issuer != f.issuer() || sc.ClientID != "astrona-cli" {
		t.Fatalf("FetchSiteConfig = %+v, %v", sc, err)
	}
	p, err := c.Discover(ctx, sc.Issuer)
	if err != nil || !strings.HasSuffix(p.TokenEndpoint, "/token") || p.RevocationEndpoint == "" {
		t.Fatalf("Discover = %+v, %v", p, err)
	}

	f.issuerName = "http://127.0.0.1:1/realms/other"
	if _, err := c.Discover(ctx, sc.Issuer); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("issuer mismatch accepted: %v", err)
	}
	f.issuerName, f.endpointHost = "", "http://localhost:9"
	if _, err := c.Discover(ctx, sc.Issuer); err == nil || !strings.Contains(err.Error(), "not on") {
		t.Errorf("endpoint on another host accepted: %v", err)
	}
	if _, err := c.Discover(ctx, "http://keycloak.example.com/realms/x"); err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("plain-http remote issuer accepted: %v", err)
	}
}

func TestRedirectToAnotherSiteIsRefused(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("followed the redirect to another host")
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1)+r.URL.Path, http.StatusFound) //nolint:gosec // test server redirecting on purpose
	}))
	defer srv.Close()
	if _, err := testClient(nil).FetchSiteConfig(context.Background(), srv.URL); err == nil || !strings.Contains(err.Error(), "refusing to follow") {
		t.Errorf("FetchSiteConfig across a redirect = %v", err)
	}
}

func deviceFlow(t *testing.T, f *fakeIdP, sleeps *[]time.Duration) (Tokens, error) {
	t.Helper()
	c := testClient(sleeps)
	ctx := context.Background()
	sc, _ := c.FetchSiteConfig(ctx, f.URL)
	p, err := c.Discover(ctx, sc.Issuer)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := c.StartDeviceFlow(ctx, p, sc.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if dc.UserCode != "ABCD-EFGH" || !strings.Contains(dc.VerificationURIComplete, "user_code=") {
		t.Fatalf("device code = %+v", dc)
	}
	return c.PollToken(ctx, p, sc.ClientID, dc)
}

func TestDeviceFlowPendingSlowDownSuccess(t *testing.T) {
	f := newFakeIdP(t)
	f.pollAnswers = []string{"authorization_pending", "slow_down", "authorization_pending", "ok"}
	var sleeps []time.Duration
	tok, err := deviceFlow(t, f, &sleeps)
	if err != nil || tok.AccessToken != "access-1" || tok.RefreshToken != "refresh-1" {
		t.Fatalf("PollToken = %+v, %v", tok, err)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second, 10 * time.Second}
	if fmt.Sprint(sleeps) != fmt.Sprint(want) {
		t.Errorf("waits = %v, want %v (slow_down adds 5s)", sleeps, want)
	}
	if got := UsernameFromTokens(tok); got != "student1" {
		t.Errorf("username = %q", got)
	}
}

func TestDeviceFlowDeniedAndExpired(t *testing.T) {
	for code, want := range map[string]error{"access_denied": ErrAccessDenied, "expired_token": ErrDeviceCodeExpired} {
		f := newFakeIdP(t)
		f.pollAnswers = []string{"authorization_pending", code}
		if _, err := deviceFlow(t, f, nil); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", code, err, want)
		}
	}

	// The code's own lifetime runs out while the user is still pending.
	f := newFakeIdP(t)
	f.pollAnswers = []string{"authorization_pending", "authorization_pending", "authorization_pending"}
	c := testClient(nil)
	start := time.Now()
	now := start
	c.Now = func() time.Time { return now }
	c.Sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return nil }
	ctx := context.Background()
	sc, _ := c.FetchSiteConfig(ctx, f.URL)
	p, _ := c.Discover(ctx, sc.Issuer)
	dc, _ := c.StartDeviceFlow(ctx, p, sc.ClientID)
	dc.ExpiresIn = 12
	if _, err := c.PollToken(ctx, p, sc.ClientID, dc); !errors.Is(err, ErrDeviceCodeExpired) {
		t.Errorf("deadline: err = %v", err)
	}
}

func TestDeviceFlowCancel(t *testing.T) {
	f := newFakeIdP(t)
	f.pollAnswers = []string{"authorization_pending", "authorization_pending"}
	c := NewClient()
	ctx, cancel := context.WithCancel(context.Background())
	sc, _ := c.FetchSiteConfig(ctx, f.URL)
	p, _ := c.Discover(ctx, sc.Issuer)
	dc, _ := c.StartDeviceFlow(ctx, p, sc.ClientID)
	cancel()
	if _, err := c.PollToken(ctx, p, sc.ClientID, dc); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled poll = %v", err)
	}
}

func TestUsernameIsDisplayOnly(t *testing.T) {
	for jwt, want := range map[string]string{
		jwtWith(map[string]string{"preferred_username": "ana"}):           "ana",
		jwtWith(map[string]string{"email": "ana@example.com"}):            "ana@example.com",
		jwtWith(map[string]string{"preferred_username": "a\x1b[31mb\nc"}): "a[31mb c",
		"not-a-jwt": "",
		"a.!!!.c":   "",
	} {
		if got := UsernameFromTokens(Tokens{IDToken: jwt}); got != want {
			t.Errorf("username(%q) = %q, want %q", jwt, got, want)
		}
	}
}

func testCreds(f *fakeIdP, expires time.Time) *Credentials {
	base := f.issuer() + "/protocol/openid-connect"
	return &Credentials{
		Site: f.URL, Issuer: f.issuer(), ClientID: "astrona-cli",
		TokenEndpoint: base + "/token", RevocationEndpoint: base + "/revoke",
		AccessToken: "access-0", RefreshToken: "refresh-0", AccessExpiresAt: expires, Username: "student1",
	}
}

func TestCredentialsFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "astrona")
	s := Store{Path: filepath.Join(dir, "credentials.json")}
	if cr, err := s.Load(); cr != nil || err != nil {
		t.Fatalf("missing file: %+v, %v", cr, err)
	}
	f := newFakeIdP(t)
	want := testCreds(f, time.Now().Add(time.Hour).UTC().Truncate(time.Second))
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || *got != *want {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %v", entries)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(s.Path)
		di, _ := os.Stat(dir)
		if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
			t.Errorf("modes: file %o, dir %o", fi.Mode().Perm(), di.Mode().Perm())
		}

		// Readable by others: refused, with the fix.
		os.Chmod(s.Path, 0o644)
		if _, err := s.Load(); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("world-readable file: %v", err)
		}
		// Save puts it back to 0600 (atomic replace).
		if err := s.Save(want); err != nil {
			t.Fatal(err)
		}
		if fi, _ := os.Stat(s.Path); fi.Mode().Perm() != 0o600 {
			t.Errorf("after re-save: %o", fi.Mode().Perm())
		}

		// A symlink is refused, not followed.
		link := Store{Path: filepath.Join(dir, "link.json")}
		os.Symlink(s.Path, link.Path)
		if _, err := link.Load(); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Errorf("symlink: %v", err)
		}
	}

	// Edited to send the tokens somewhere else: refused.
	bad := *want
	bad.TokenEndpoint = "https://evil.example.com/token"
	if err := s.Save(&bad); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil || !strings.Contains(err.Error(), "astrona logout") {
		t.Errorf("foreign token endpoint: %v", err)
	}

	os.WriteFile(s.Path, []byte("{not json"), 0o600)
	if _, err := s.Load(); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Errorf("damaged: %v", err)
	}
	if existed, err := s.Delete(); !existed || err != nil {
		t.Errorf("Delete = %v, %v", existed, err)
	}
	if existed, err := s.Delete(); existed || err != nil {
		t.Errorf("second Delete = %v, %v", existed, err)
	}
}

func TestActiveRefreshes(t *testing.T) {
	f := newFakeIdP(t)
	s := Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	c := testClient(nil)
	ctx := context.Background()

	if _, err := c.Active(ctx, s, f.URL); !errors.Is(err, ErrSignedOut) {
		t.Errorf("no file: %v", err)
	}

	// Fresh: used as is.
	s.Save(testCreds(f, time.Now().Add(time.Hour)))
	if cr, err := c.Active(ctx, s, f.URL); err != nil || cr.AccessToken != "access-0" || f.refreshCalls != 0 {
		t.Fatalf("fresh = %+v, %v (refreshes %d)", cr, err, f.refreshCalls)
	}
	// Signed in to another site: signed out here, no token sent anywhere.
	if _, err := c.Active(ctx, s, "https://astrona.io"); !errors.Is(err, ErrSignedOut) || !strings.Contains(err.Error(), "signed in to") {
		t.Errorf("other site: %v", err)
	}

	// Expired: renewed and saved.
	s.Save(testCreds(f, time.Now().Add(-time.Minute)))
	cr, err := c.Active(ctx, s, f.URL)
	if err != nil || cr.AccessToken != "access-r1" || cr.RefreshToken != "refresh-r1" {
		t.Fatalf("expired = %+v, %v", cr, err)
	}
	if saved, _ := s.Load(); saved.AccessToken != "access-r1" || !saved.fresh(time.Now()) {
		t.Errorf("renewed credentials not saved: %+v", saved)
	}

	// Refresh token rejected: signed out, stale file removed.
	s.Save(testCreds(f, time.Now().Add(-time.Minute)))
	f.refresh = "invalid_grant"
	if _, err := c.Active(ctx, s, f.URL); !errors.Is(err, ErrSignedOut) {
		t.Errorf("invalid_grant: %v", err)
	}
	if cr, _ := s.Load(); cr != nil {
		t.Error("stale credentials kept after invalid_grant")
	}

	// Any other refresh failure is an error, and the sign-in is kept.
	s.Save(testCreds(f, time.Now().Add(-time.Minute)))
	f.refresh = "temporarily_unavailable"
	if _, err := c.Active(ctx, s, f.URL); err == nil || errors.Is(err, ErrSignedOut) {
		t.Errorf("server trouble: %v", err)
	}
	if cr, _ := s.Load(); cr == nil {
		t.Error("credentials removed on a transient failure")
	}
}

func TestCreateLabSession(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T, statuses ...int) (*fakeIdP, Store, *Credentials) {
		f := newFakeIdP(t)
		f.sessions = statuses
		s := Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
		cr := testCreds(f, time.Now().Add(time.Hour))
		s.Save(cr)
		return f, s, cr
	}

	f, s, cr := setup(t)
	ls, err := testClient(nil).CreateLabSession(ctx, s, cr, "ATS014/section-010/module-01/lab-02")
	if err != nil || ls.Lab != "ATS014/section-010/module-01/lab-02" || !strings.HasPrefix(ls.URL, f.URL+"/labs/") {
		t.Fatalf("session = %+v, %v", ls, err)
	}
	if f.sessionAuths[0] != "Bearer access-0" {
		t.Errorf("Authorization = %q", f.sessionAuths[0])
	}

	// 401 once: renewed, retried with the new token.
	f, s, cr = setup(t, 401)
	if _, err := testClient(nil).CreateLabSession(ctx, s, cr, "L"); err != nil {
		t.Fatalf("401 then ok: %v", err)
	}
	if len(f.sessionAuths) != 2 || f.sessionAuths[1] != "Bearer access-r1" {
		t.Errorf("retry auths = %v", f.sessionAuths)
	}

	for _, c := range []struct {
		statuses []int
		want     error
		text     string
	}{
		{[]int{401, 401}, ErrSignedOut, "didn't accept"},
		{[]int{429}, ErrTooManySessions, "server says 429"},
		{[]int{422}, ErrUnknownLab, "server says 422"},
	} {
		_, s, cr := setup(t, c.statuses...)
		_, err := testClient(nil).CreateLabSession(ctx, s, cr, "L")
		if !errors.Is(err, c.want) || !strings.Contains(err.Error(), c.text) {
			t.Errorf("%v: err = %v", c.statuses, err)
		}
	}
	_, s, cr = setup(t, 503)
	if _, err := testClient(nil).CreateLabSession(ctx, s, cr, "L"); err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("503: %v", err)
	}
	// 401 and the refresh token is rejected too: signed out.
	f, s, cr = setup(t, 401)
	f.refresh = "invalid_grant"
	if _, err := testClient(nil).CreateLabSession(ctx, s, cr, "L"); !errors.Is(err, ErrSignedOut) {
		t.Errorf("401 + invalid_grant: %v", err)
	}
}

func TestErrorsNeverEchoTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"oops":"access-0 refresh-0"}`))
	}))
	defer srv.Close()
	cr := &Credentials{Site: srv.URL, AccessToken: "access-0", RefreshToken: "refresh-0"}
	_, err := testClient(nil).CreateLabSession(context.Background(), Store{Path: filepath.Join(t.TempDir(), "c")}, cr, "L")
	if err == nil || strings.Contains(err.Error(), "access-0") || strings.Contains(err.Error(), "refresh-0") {
		t.Errorf("err = %v", err)
	}
}

func TestSameSite(t *testing.T) {
	for page, want := range map[string]bool{
		"https://astrona.io/labs/x":        true,
		"https://www.astrona.io/labs/x":    true,
		"http://astrona.io/labs/x":         false,
		"https://astrona.io.evil.com/labs": false,
		"https://astrona.io:8443/labs":     false,
	} {
		u, _ := CheckHTTPURL(page)
		if got := SameSite(u, "https://astrona.io"); got != want {
			t.Errorf("SameSite(%s) = %v", page, got)
		}
	}
}
