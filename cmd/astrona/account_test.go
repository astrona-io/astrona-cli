package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"astrona/internal/account"
	"astrona/internal/catalog"
)

// fakeAstrona is a site + issuer: config, discovery, device flow (signed
// in on the first poll), refresh, revoke and lab sessions.
func fakeAstrona(t *testing.T, sessionStatus int, pageURL func(base string) string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := srv.URL + "/realms/a/protocol/openid-connect"
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/cli/config":
			fmt.Fprintf(w, `{"issuer":%q,"client_id":"astrona-cli"}`, srv.URL+"/realms/a")
		case "/realms/a/.well-known/openid-configuration":
			fmt.Fprintf(w, `{"issuer":%q,"device_authorization_endpoint":%q,"token_endpoint":%q,"revocation_endpoint":%q}`,
				srv.URL+"/realms/a", base+"/auth/device", base+"/token", base+"/revoke")
		case "/realms/a/protocol/openid-connect/auth/device":
			fmt.Fprintf(w, `{"device_code":"d","user_code":"WXYZ-1234","verification_uri":%q,"verification_uri_complete":%q,"expires_in":60,"interval":1}`,
				srv.URL+"/device", srv.URL+"/device?code=WXYZ-1234")
		case "/realms/a/protocol/openid-connect/token":
			claims := base64.RawURLEncoding.EncodeToString([]byte(`{"preferred_username":"student1"}`))
			fmt.Fprintf(w, `{"access_token":"secret-access","refresh_token":"secret-refresh","token_type":"Bearer","expires_in":300,"id_token":"e30.%s.x"}`, claims)
		case "/realms/a/protocol/openid-connect/revoke":
		case "/api/cli/lab-sessions":
			if sessionStatus != http.StatusCreated {
				w.WriteHeader(sessionStatus)
				fmt.Fprintf(w, `{"statusMessage":"status %d"}`, sessionStatus)
				return
			}
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"1","token":"t","lab":"L","username":"student1","expires_at":"x","url":%q}`, pageURL(srv.URL))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withAccount points the sign-in gate at srv and a credentials file in a
// temp dir, and keeps the browser closed.
func withAccount(t *testing.T, site string) (*account.Client, account.Store) {
	t.Helper()
	client := account.NewClient()
	client.Sleep = func(ctx context.Context, d time.Duration) error { return ctx.Err() }
	store := account.Store{Path: filepath.Join(t.TempDir(), ".astrona", "credentials.json")}
	prevDeps, prevOpen := accountDeps, browserOpener
	accountDeps = func() (*account.Client, account.Store, string, error) { return client, store, site, nil }
	var opened []string
	browserOpener = func(u string) bool { opened = append(opened, u); return false }
	t.Cleanup(func() { accountDeps, browserOpener = prevDeps, prevOpen })
	return client, store
}

func TestRequireSignInOnlyForCatalogLabs(t *testing.T) {
	accountDepsCalled := false
	prev := accountDeps
	accountDeps = func() (*account.Client, account.Store, string, error) {
		accountDepsCalled = true
		return nil, account.Store{}, "", errors.New("should not be called")
	}
	defer func() { accountDeps = prev }()
	// -c / --git labs: no sign-in, no network, no credentials read.
	acct, err := requireSignIn(context.Background(), &rootFlags{configPath: "./labs/x"}, "run")
	if acct != nil || err != nil || accountDepsCalled {
		t.Errorf("-c lab: %v, %v (deps called %v)", acct, err, accountDepsCalled)
	}

	srv := fakeAstrona(t, http.StatusCreated, nil)
	withAccount(t, srv.URL)
	lab := "ATS014/section-010/module-01/lab-02"
	_, err = requireSignIn(context.Background(), &rootFlags{catalogLab: lab}, "run")
	if err == nil {
		t.Fatal("catalog lab without credentials was allowed")
	}
	for _, want := range []string{"You're not signed in to Astrona.", `run "astrona login" first`, `"astrona run ` + lab + `" again`, "-c / --git"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%s", want, err)
		}
	}
}

func TestCatalogNameMarksTheLab(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	store := catalog.Store{Dir: filepath.Join(home, ".astrona")}
	store.Save(catalog.Catalog{FetchedAt: time.Now(), Trainings: []catalog.Training{{
		ID: "ATS014", Repo: "https://github.com/astrona-io/ATS014.git",
		Labs: []catalog.Lab{{ID: "ATS014/section-010/module-01/lab-01", Path: "sections/section-010/module-01/labs/lab-01"}},
	}}})

	f := &rootFlags{configPath: ".", fileName: "config.yaml"}
	if err := labArg([]string{"ats014/section-010/module-01/lab-01"}, f); err != nil {
		t.Fatal(err)
	}
	if f.catalogLab != "ATS014/section-010/module-01/lab-01" {
		t.Errorf("catalogLab = %q", f.catalogLab)
	}
	// A path or git URL argument is not a catalog lab, even after one was remembered.
	for _, arg := range []string{"./labs/x", "https://github.com/org/labs"} {
		g := &rootFlags{catalogLab: "ATS014/x/y/z"}
		if err := labArg([]string{arg}, g); err != nil || g.catalogLab != "" {
			t.Errorf("labArg(%q): catalogLab = %q, %v", arg, g.catalogLab, err)
		}
	}
	// `astrona use <catalog lab>` remembers that it is one.
	if err := saveCurrentLab(&currentLab{Config: "sections/x", Git: "https://github.com/astrona-io/ATS014.git", Catalog: "ATS014/section-010/module-01/lab-01"}); err != nil {
		t.Fatal(err)
	}
	cmd := newRunCmd(&rootFlags{})
	h := &rootFlags{fileName: "config.yaml"}
	applyCurrentLab(cmd, h)
	if h.catalogLab != "ATS014/section-010/module-01/lab-01" {
		t.Errorf("remembered catalogLab = %q", h.catalogLab)
	}
}

func TestLoginWhoamiLogout(t *testing.T) {
	srv := fakeAstrona(t, http.StatusCreated, nil)
	client, store := withAccount(t, srv.URL)
	ctx := context.Background()

	if err := whoami(ctx, client, store, srv.URL); err == nil || !strings.Contains(err.Error(), "not signed in") || !strings.Contains(err.Error(), "astrona login") {
		t.Errorf("whoami signed out: %v", err)
	}
	out := captureStdout(t, func() {
		if err := login(ctx, client, store, srv.URL); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"enter code WXYZ-1234", "Signed in as student1 on " + srv.URL} {
		if !strings.Contains(out, want) {
			t.Errorf("login output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-") {
		t.Errorf("login printed a token:\n%s", out)
	}
	data, _ := os.ReadFile(store.Path)
	if !strings.Contains(string(data), "secret-refresh") {
		t.Error("credentials not saved")
	}

	out = captureStdout(t, func() {
		if err := whoami(ctx, client, store, srv.URL); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "Signed in as student1 on "+srv.URL {
		t.Errorf("whoami = %q", out)
	}
	if err := whoami(ctx, client, store, "https://astrona.io"); err == nil || !strings.Contains(err.Error(), "signed in to "+srv.URL) {
		t.Errorf("whoami for another site: %v", err)
	}

	out = captureStdout(t, func() {
		if err := logout(client, store); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Signed out student1") || !strings.Contains(out, "revoked on the server") {
		t.Errorf("logout = %q", out)
	}
	if _, err := os.Stat(store.Path); !os.IsNotExist(err) {
		t.Error("credentials file still there after logout")
	}
	out = captureStdout(t, func() { _ = logout(client, store) })
	if !strings.Contains(out, "not signed in") {
		t.Errorf("second logout = %q", out)
	}
}

func TestStartSession(t *testing.T) {
	signIn := func(t *testing.T, srv *httptest.Server) *labAccount {
		client, store := withAccount(t, srv.URL)
		captureStdout(t, func() {
			if err := login(context.Background(), client, store, srv.URL); err != nil {
				t.Fatal(err)
			}
		})
		acct, err := requireSignIn(context.Background(), &rootFlags{catalogLab: "L"}, "run")
		if err != nil || acct == nil {
			t.Fatalf("requireSignIn = %v, %v", acct, err)
		}
		return acct
	}

	srv := fakeAstrona(t, http.StatusCreated, func(base string) string { return base + "/labs/L?t=abc" })
	acct := signIn(t, srv)
	var page fmt.Stringer
	captureStdout(t, func() {
		p, err := acct.startSession(context.Background(), "run", "L")
		if err != nil {
			t.Fatal(err)
		}
		page = p
	})
	if page.String() != srv.URL+"/labs/L?t=abc" {
		t.Errorf("page = %s", page)
	}

	// A page on another site is never opened.
	other := fakeAstrona(t, http.StatusCreated, func(string) string { return "https://evil.example.com/labs/L" })
	if _, err := signIn(t, other).startSession(context.Background(), "run", "L"); err == nil || !strings.Contains(err.Error(), "another site") {
		t.Errorf("foreign page: %v", err)
	}
	bad := fakeAstrona(t, http.StatusCreated, func(string) string { return "javascript:alert(1)" })
	if _, err := signIn(t, bad).startSession(context.Background(), "run", "L"); err == nil || !strings.Contains(err.Error(), "nothing was built") {
		t.Errorf("bad page URL: %v", err)
	}

	for status, want := range map[int]string{
		http.StatusTooManyRequests:     "won't start another lab session",
		http.StatusUnprocessableEntity: "doesn't recognise the lab L",
		http.StatusUnauthorized:        `run "astrona login" first`,
		http.StatusServiceUnavailable:  "nothing was built",
	} {
		s := fakeAstrona(t, status, nil)
		_, err := signIn(t, s).startSession(context.Background(), "run", "L")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("HTTP %d: %v, want %q", status, err, want)
		}
	}
}

func TestNotSignedInReasons(t *testing.T) {
	expired := fmt.Errorf("your sign-in has expired or was revoked: %w", account.ErrSignedOut)
	if got := notSignedInError(expired, "reset", "L").Error(); !strings.HasPrefix(got, "Your sign-in has expired or was revoked. Catalog labs") ||
		!strings.Contains(got, `"astrona reset L" again`) {
		t.Errorf("expired: %s", got)
	}
}

// A site without astrona sign-in (404 on /api/cli/config) says how to sign
// in to another one.
func TestLoginSiteWithoutSignIn(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	store := account.Store{Path: filepath.Join(t.TempDir(), "credentials.json")}
	err := login(context.Background(), account.NewClient(), store, srv.URL)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "astrona login --site http://localhost:3000") {
		t.Errorf("err = %v", err)
	}
}
