package account

import (
	"context"
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

// fakeSite is an Astrona site: CLI config, device sign-in, token,
// revocation and lab-session endpoints.
type fakeSite struct {
	*httptest.Server
	mu sync.Mutex
	// config overrides /api/cli/config's answer.
	config string
	// pageURL overrides verification_uri_complete.
	pageURL string
	devices []Device
	// pollAnswers are the token endpoint's device-code answers, in order.
	pollAnswers []string
	// refresh answers refresh_token grants: "" = new tokens, else an error code.
	refresh       string
	refreshCalls  int
	refreshTokens []string
	revoked       []string
	// sessions are the lab-session endpoint's statuses, in order (then 201).
	sessions     []int
	sessionAuths []string
	// results are the result endpoint's statuses, in order (then 201).
	results     []int
	resultAuths []string
	resultPaths []string
	resultBody  []byte
	// goneAsDetail answers 410 in the labs service's {"detail"} shape.
	goneAsDetail bool
}

func newFakeSite(t *testing.T) *fakeSite {
	f := &fakeSite{}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeSite) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	oauthErr := func(code string) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":%q,"error_description":"desc for %s"}`, code, code)
	}
	tokens := func(access, refresh string) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": access, "token_type": "Bearer", "expires_in": 300, "refresh_token": refresh,
			"username": "student1", "device_id": "dev-42",
		})
	}
	if strings.HasPrefix(r.URL.Path, "/api/cli/lab-sessions/") {
		f.handleResult(w, r)
		return
	}
	var body map[string]string
	if r.Method == http.MethodPost {
		if r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
			oauthErr("invalid_request")
			return
		}
	}
	switch r.URL.Path {
	case "/api/cli/config":
		if f.config != "" {
			fmt.Fprint(w, f.config)
			return
		}
		fmt.Fprint(w, `{"device_endpoint":"/api/cli/device","token_endpoint":"/api/cli/token","revocation_endpoint":"/api/cli/revoke"}`)
	case "/api/cli/device":
		f.devices = append(f.devices, Device{Name: body["device_name"], OS: body["os"]})
		page := f.pageURL
		if page == "" {
			page = f.URL + "/cli/authorize?code=ABCD-EFGH"
		}
		fmt.Fprintf(w, `{"device_code":"dev-123","user_code":"ABCD-EFGH","verification_uri":%q,"verification_uri_complete":%q,"expires_in":600,"interval":5}`,
			f.URL+"/cli/authorize", page)
	case "/api/cli/token":
		switch body["grant_type"] {
		case "device_code":
			if body["device_code"] != "dev-123" {
				oauthErr("invalid_grant")
				return
			}
			next := "ok"
			if len(f.pollAnswers) > 0 {
				next, f.pollAnswers = f.pollAnswers[0], f.pollAnswers[1:]
			}
			if next != "ok" {
				oauthErr(next)
				return
			}
			tokens("access-1", "refresh-1")
		case "refresh_token":
			f.refreshCalls++
			f.refreshTokens = append(f.refreshTokens, body["refresh_token"])
			if f.refresh != "" {
				oauthErr(f.refresh)
				return
			}
			tokens(fmt.Sprintf("access-r%d", f.refreshCalls), fmt.Sprintf("refresh-r%d", f.refreshCalls))
		default:
			oauthErr("unsupported_grant_type")
		}
	case "/api/cli/revoke":
		f.revoked = append(f.revoked, body["refresh_token"])
		w.WriteHeader(http.StatusNoContent)
	case "/api/cli/lab-sessions":
		f.sessionAuths = append(f.sessionAuths, r.Header.Get("Authorization"))
		status := http.StatusCreated
		if len(f.sessions) > 0 {
			status, f.sessions = f.sessions[0], f.sessions[1:]
		}
		w.WriteHeader(status)
		if status != http.StatusCreated {
			fmt.Fprintf(w, `{"statusCode":%d,"statusMessage":"server says %d"}`, status, status)
			return
		}
		fmt.Fprintf(w, `{"id":"s1","token":"tok","lab":%q,"username":"student1","expires_at":"2026-10-07T20:00:00","max_minutes":60,"url":%q}`,
			body["lab"], f.URL+"/labs/"+body["lab"]+"?t=tok")
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
		if got, err := ResolveSite(env(raw), ""); err != nil || got != want {
			t.Errorf("ResolveSite(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"http://astrona.io", "ftp://astrona.io", "https://u:p@astrona.io", "https://astrona.io/app", "astrona.io", "https://astrona.io?x=1"} {
		if got, err := ResolveSite(env(raw), ""); err == nil {
			t.Errorf("ResolveSite(%q) = %q, want an error", raw, got)
		}
	}
}

// Without ASTRONA_URL the site of the saved sign-in is used; ASTRONA_URL
// still wins; an invalid saved site falls back to the default.
func TestResolveSiteSaved(t *testing.T) {
	none := func(string) string { return "" }
	local := "http://localhost:3000"
	if got, _ := ResolveSite(none, local); got != local {
		t.Errorf("saved site = %q, want %q", got, local)
	}
	staging := func(string) string { return "https://staging.astrona.io" }
	if got, _ := ResolveSite(staging, local); got != "https://staging.astrona.io" {
		t.Errorf("ASTRONA_URL over saved = %q", got)
	}
	if got, _ := ResolveSite(none, "http://evil.example"); got != DefaultSite {
		t.Errorf("invalid saved site = %q, want the default", got)
	}
	if _, err := ResolveSite(func(string) string { return "ftp://x" }, ""); err == nil || !strings.Contains(err.Error(), SiteEnv) {
		t.Errorf("bad ASTRONA_URL error = %v, want it to name %s", err, SiteEnv)
	}
}

func TestSiteConfig(t *testing.T) {
	f := newFakeSite(t)
	c := testClient(nil)
	ctx := context.Background()
	sc, err := c.FetchSiteConfig(ctx, f.URL)
	if err != nil || sc.DeviceEndpoint != f.URL+"/api/cli/device" || sc.TokenEndpoint != f.URL+"/api/cli/token" ||
		sc.RevocationEndpoint != f.URL+"/api/cli/revoke" {
		t.Fatalf("FetchSiteConfig = %+v, %v", sc, err)
	}

	// Any endpoint that resolves off the site is refused.
	for _, cfg := range []string{
		`{"device_endpoint":"https://evil.example.com/device","token_endpoint":"/api/cli/token"}`,
		`{"device_endpoint":"/api/cli/device","token_endpoint":"//evil.example.com/token"}`,
		`{"device_endpoint":"/api/cli/device","token_endpoint":"/api/cli/token","revocation_endpoint":"http://localhost:9/revoke"}`,
		`{"device_endpoint":"/api/cli/device","token_endpoint":"https://u:p@` + strings.TrimPrefix(f.URL, "http://") + `/t"}`,
	} {
		f.config = cfg
		if _, err := c.FetchSiteConfig(ctx, f.URL); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("%s accepted: %v", cfg, err)
		}
	}
	f.config = `{"token_endpoint":"/api/cli/token"}`
	if _, err := c.FetchSiteConfig(ctx, f.URL); err == nil || !strings.Contains(err.Error(), "device_endpoint is missing") {
		t.Errorf("missing device endpoint: %v", err)
	}
	// The revocation endpoint is optional.
	f.config = `{"device_endpoint":"/api/cli/device","token_endpoint":"/api/cli/token"}`
	if sc, err := c.FetchSiteConfig(ctx, f.URL); err != nil || sc.RevocationEndpoint != "" {
		t.Errorf("no revocation endpoint: %+v, %v", sc, err)
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

var testDevice = Device{Name: "ana-laptop", OS: "linux-wsl"}

func deviceFlow(t *testing.T, f *fakeSite, sleeps *[]time.Duration) (Tokens, error) {
	t.Helper()
	c := testClient(sleeps)
	ctx := context.Background()
	sc, err := c.FetchSiteConfig(ctx, f.URL)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := c.StartDeviceFlow(ctx, f.URL, sc, testDevice)
	if err != nil {
		t.Fatal(err)
	}
	if dc.UserCode != "ABCD-EFGH" || dc.VerificationURIComplete != f.URL+"/cli/authorize?code=ABCD-EFGH" {
		t.Fatalf("device code = %+v", dc)
	}
	if len(f.devices) != 1 || f.devices[0] != testDevice {
		t.Errorf("device sent = %+v", f.devices)
	}
	return c.PollToken(ctx, sc, dc)
}

func TestDeviceFlowPendingSlowDownSuccess(t *testing.T) {
	f := newFakeSite(t)
	f.pollAnswers = []string{"authorization_pending", "slow_down", "authorization_pending", "ok"}
	var sleeps []time.Duration
	tok, err := deviceFlow(t, f, &sleeps)
	if err != nil || tok.AccessToken != "access-1" || tok.RefreshToken != "refresh-1" || tok.Username != "student1" || tok.DeviceID != "dev-42" {
		t.Fatalf("PollToken = %+v, %v", tok, err)
	}
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second, 10 * time.Second}
	if fmt.Sprint(sleeps) != fmt.Sprint(want) {
		t.Errorf("waits = %v, want %v (slow_down adds 5s)", sleeps, want)
	}
}

func TestDeviceFlowSignInPageMustBeOnTheSite(t *testing.T) {
	c := testClient(nil)
	ctx := context.Background()
	for _, page := range []string{"https://evil.example.com/cli/authorize", "javascript:alert(1)", "/cli/authorize"} {
		f := newFakeSite(t)
		f.pageURL = page
		sc, _ := c.FetchSiteConfig(ctx, f.URL)
		if _, err := c.StartDeviceFlow(ctx, f.URL, sc, testDevice); err == nil {
			t.Errorf("sign-in page %q accepted", page)
		}
	}
}

func TestDeviceFlowDeniedAndExpired(t *testing.T) {
	for code, want := range map[string]error{"access_denied": ErrAccessDenied, "expired_token": ErrDeviceCodeExpired} {
		f := newFakeSite(t)
		f.pollAnswers = []string{"authorization_pending", code}
		if _, err := deviceFlow(t, f, nil); !errors.Is(err, want) {
			t.Errorf("%s: err = %v, want %v", code, err, want)
		}
	}

	// The code's own lifetime runs out while the student is still pending.
	f := newFakeSite(t)
	f.pollAnswers = []string{"authorization_pending", "authorization_pending", "authorization_pending"}
	c := testClient(nil)
	now := time.Now()
	c.Now = func() time.Time { return now }
	c.Sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return nil }
	ctx := context.Background()
	sc, _ := c.FetchSiteConfig(ctx, f.URL)
	dc, _ := c.StartDeviceFlow(ctx, f.URL, sc, testDevice)
	dc.ExpiresIn = 12
	if _, err := c.PollToken(ctx, sc, dc); !errors.Is(err, ErrDeviceCodeExpired) {
		t.Errorf("deadline: err = %v", err)
	}
}

func TestDeviceFlowCancel(t *testing.T) {
	f := newFakeSite(t)
	f.pollAnswers = []string{"authorization_pending", "authorization_pending"}
	c := NewClient()
	ctx, cancel := context.WithCancel(context.Background())
	sc, _ := c.FetchSiteConfig(ctx, f.URL)
	dc, _ := c.StartDeviceFlow(ctx, f.URL, sc, testDevice)
	cancel()
	if _, err := c.PollToken(ctx, sc, dc); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled poll = %v", err)
	}
}

func TestDeviceName(t *testing.T) {
	long := strings.Repeat("x", 150)
	for in, want := range map[string]string{
		"ana-laptop.local": "ana-laptop.local",
		"  box\x1b[31m\n":  "box[31m",
		long:               long[:100],
		"":                 "unknown",
	} {
		if got := DeviceName(in); got != want {
			t.Errorf("DeviceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRevoke(t *testing.T) {
	f := newFakeSite(t)
	if err := testClient(nil).Revoke(context.Background(), f.URL, "refresh-0"); err != nil {
		t.Fatal(err)
	}
	if len(f.revoked) != 1 || f.revoked[0] != "refresh-0" {
		t.Errorf("revoked = %v", f.revoked)
	}
	f.config = `{"device_endpoint":"/api/cli/device","token_endpoint":"/api/cli/token"}`
	if err := testClient(nil).Revoke(context.Background(), f.URL, "refresh-0"); err == nil {
		t.Error("revoke without an endpoint succeeded")
	}
}

func testCreds(f *fakeSite, expires time.Time) *Credentials {
	return &Credentials{
		Site: f.URL, AccessToken: "access-0", RefreshToken: "refresh-0", AccessExpiresAt: expires,
		Username: "student1", DeviceID: "dev-42", DeviceName: "ana-laptop",
	}
}

func TestCredentialsFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "astrona")
	s := Store{Path: filepath.Join(dir, "credentials.json")}
	if cr, err := s.Load(); cr != nil || err != nil {
		t.Fatalf("missing file: %+v, %v", cr, err)
	}
	f := newFakeSite(t)
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

	// Edited to send the tokens to a plain-http remote site: refused.
	bad := *want
	bad.Site = "http://evil.example.com"
	if err := s.Save(&bad); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil || !strings.Contains(err.Error(), "astrona logout") {
		t.Errorf("plain-http remote site: %v", err)
	}
	// Terminal escapes in display fields are stripped on load.
	esc := *want
	esc.Username = "ana\x1b[2J"
	s.Save(&esc)
	if got, err := s.Load(); err != nil || got.Username != "ana[2J" {
		t.Errorf("escaped username = %+v, %v", got, err)
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
	f := newFakeSite(t)
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

	// Expired: renewed, and the rotated refresh token saved.
	s.Save(testCreds(f, time.Now().Add(-time.Minute)))
	cr, err := c.Active(ctx, s, f.URL)
	if err != nil || cr.AccessToken != "access-r1" || cr.RefreshToken != "refresh-r1" || cr.DeviceName != "ana-laptop" {
		t.Fatalf("expired = %+v, %v", cr, err)
	}
	if f.refreshTokens[0] != "refresh-0" {
		t.Errorf("refreshed with %q", f.refreshTokens[0])
	}
	if saved, _ := s.Load(); saved.AccessToken != "access-r1" || saved.RefreshToken != "refresh-r1" || !saved.fresh(time.Now()) {
		t.Errorf("renewed credentials not saved: %+v", saved)
	}
	// The next renewal uses the rotated token, never the old one.
	if _, err := c.Renew(ctx, s, cr); err != nil || f.refreshTokens[1] != "refresh-r1" {
		t.Errorf("second renewal used %v, %v", f.refreshTokens, err)
	}
	if saved, _ := s.Load(); saved.RefreshToken != "refresh-r2" {
		t.Errorf("second rotation not saved: %q", saved.RefreshToken)
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

	// Rejected because another astrona already rotated it: that one's
	// saved sign-in is used, not deleted.
	stale := testCreds(f, time.Now().Add(-time.Minute))
	rotated := testCreds(f, time.Now().Add(time.Hour))
	rotated.AccessToken, rotated.RefreshToken = "access-other", "refresh-other"
	s.Save(rotated)
	if cr, err := c.Renew(ctx, s, stale); err != nil || cr.AccessToken != "access-other" {
		t.Errorf("concurrent rotation = %+v, %v", cr, err)
	}
	if cr, _ := s.Load(); cr == nil {
		t.Error("credentials removed after a concurrent rotation")
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

func TestOldCredentialsFormatIsSignedOut(t *testing.T) {
	f := newFakeSite(t)
	s := Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	old := fmt.Sprintf(`{"site":%q,"issuer":%q,"client_id":"astrona-cli","token_endpoint":%q,"access_token":"a","refresh_token":"r","access_expires_at":"2099-01-01T00:00:00Z"}`,
		f.URL, f.URL+"/realms/astrona", f.URL+"/realms/astrona/protocol/openid-connect/token")
	if err := os.WriteFile(s.Path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if cr, err := s.Load(); cr != nil || !errors.Is(err, ErrSignedOut) || !strings.Contains(err.Error(), "older version") {
		t.Errorf("Load = %+v, %v", cr, err)
	}
	if _, err := testClient(nil).Active(context.Background(), s, f.URL); !errors.Is(err, ErrSignedOut) {
		t.Errorf("Active = %v", err)
	}
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Error("old credentials file kept")
	}
	if f.refreshCalls != 0 || len(f.revoked) != 0 {
		t.Error("old tokens were sent to the site")
	}
}

func TestCreateLabSession(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T, statuses ...int) (*fakeSite, Store, *Credentials) {
		f := newFakeSite(t)
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
	if ls.MaxMinutes != 60 {
		t.Errorf("MaxMinutes = %d", ls.MaxMinutes)
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
