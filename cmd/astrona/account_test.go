package main

import (
	"bytes"
	"context"
	"encoding/json"
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

// fakeAstrona is an Astrona site: CLI config, device sign-in (authorized on
// the first poll), token, revocation and lab sessions.
func fakeAstrona(t *testing.T, sessionStatus int, pageURL func(base string) string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/cli/config":
			fmt.Fprint(w, `{"device_endpoint":"/api/cli/device","token_endpoint":"/api/cli/token","revocation_endpoint":"/api/cli/revoke"}`)
		case "/api/cli/device":
			fmt.Fprintf(w, `{"device_code":"d","user_code":"WXYZ-1234","verification_uri":%q,"verification_uri_complete":%q,"expires_in":60,"interval":1}`,
				srv.URL+"/cli/authorize", srv.URL+"/cli/authorize?code=WXYZ-1234")
		case "/api/cli/token":
			fmt.Fprint(w, `{"access_token":"secret-access","token_type":"Bearer","expires_in":300,"refresh_token":"secret-refresh","username":"student1","device_id":"dev-1"}`)
		case "/api/cli/revoke":
			w.WriteHeader(http.StatusNoContent)
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

var testDevice = account.Device{Name: "ana-laptop", OS: "darwin"}

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

	if err := whoami(ctx, client, store, srv.URL, false); err == nil || !strings.Contains(err.Error(), "not signed in") || !strings.Contains(err.Error(), "astrona login") {
		t.Errorf("whoami signed out: %v", err)
	}
	out := captureStdout(t, func() {
		if err := whoami(ctx, client, store, srv.URL, true); err == nil {
			t.Error("whoami -o json signed out: want an error (exit 1)")
		}
	})
	if want := `{"signedIn":false,"site":"` + srv.URL + `"}`; compactJSON(t, out) != want {
		t.Errorf("whoami -o json signed out = %s, want %s", out, want)
	}
	out = captureStdout(t, func() {
		if err := login(ctx, client, store, srv.URL, testDevice); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{
		"Opening your browser to sign in… If it doesn't open, go to " + srv.URL + "/cli/authorize?code=WXYZ-1234",
		"Confirm the code WXYZ-1234 matches the one in your browser.",
		"Signed in as student1 on " + srv.URL,
	} {
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
		if err := whoami(ctx, client, store, srv.URL, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(out) != "Signed in as student1 on "+srv.URL {
		t.Errorf("whoami = %q", out)
	}
	out = captureStdout(t, func() {
		if err := whoami(ctx, client, store, srv.URL, true); err != nil {
			t.Fatal(err)
		}
	})
	if want := `{"signedIn":true,"username":"student1","site":"` + srv.URL + `"}`; compactJSON(t, out) != want {
		t.Errorf("whoami -o json = %s, want %s", out, want)
	}
	if err := whoami(ctx, client, store, "https://astrona.io", false); err == nil || !strings.Contains(err.Error(), "signed in to "+srv.URL) {
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
			if err := login(context.Background(), client, store, srv.URL, testDevice); err != nil {
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
		p, _, err := acct.startSession(context.Background(), "run", "L")
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
	if _, _, err := signIn(t, other).startSession(context.Background(), "run", "L"); err == nil || !strings.Contains(err.Error(), "another site") {
		t.Errorf("foreign page: %v", err)
	}
	bad := fakeAstrona(t, http.StatusCreated, func(string) string { return "javascript:alert(1)" })
	if _, _, err := signIn(t, bad).startSession(context.Background(), "run", "L"); err == nil || !strings.Contains(err.Error(), "nothing was built") {
		t.Errorf("bad page URL: %v", err)
	}

	for status, want := range map[int]string{
		http.StatusTooManyRequests:     "won't start another lab session",
		http.StatusUnprocessableEntity: "doesn't recognise the lab L",
		http.StatusUnauthorized:        `run "astrona login" first`,
		http.StatusServiceUnavailable:  "nothing was built",
	} {
		s := fakeAstrona(t, status, nil)
		_, _, err := signIn(t, s).startSession(context.Background(), "run", "L")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("HTTP %d: %v, want %q", status, err, want)
		}
	}
}

func TestDeviceOS(t *testing.T) {
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	for _, c := range []struct {
		goos string
		env  map[string]string
		want string
	}{
		{"darwin", nil, "darwin"},
		{"windows", nil, "windows"},
		{"linux", nil, "linux"},
		{"linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, "linux-wsl"},
	} {
		if got := deviceOS(c.goos, env(c.env)); got != c.want {
			t.Errorf("deviceOS(%s, %v) = %q, want %q", c.goos, c.env, got, c.want)
		}
	}
}

func TestLoginDeclinedAndOldCredentials(t *testing.T) {
	// Declined in the browser: the student's own words, nothing saved.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/cli/config":
			fmt.Fprint(w, `{"device_endpoint":"/d","token_endpoint":"/t"}`)
		case "/d":
			fmt.Fprintf(w, `{"device_code":"d","user_code":"C","verification_uri_complete":%q,"expires_in":60,"interval":1}`, srv.URL+"/a")
		case "/t":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"access_denied"}`)
		}
	}))
	defer srv.Close()
	client, store := withAccount(t, srv.URL)
	captureStdout(t, func() {
		if err := login(context.Background(), client, store, srv.URL, testDevice); err == nil || err.Error() != "Sign-in was declined in the browser." {
			t.Errorf("declined: %v", err)
		}
	})
	if _, err := os.Stat(store.Path); !os.IsNotExist(err) {
		t.Error("credentials saved after a declined sign-in")
	}

	// A credentials file from the Keycloak-era astrona: signed out, told to log in.
	os.MkdirAll(filepath.Dir(store.Path), 0o700)
	os.WriteFile(store.Path, []byte(`{"site":"`+srv.URL+`","issuer":"x","client_id":"astrona-cli","access_token":"a"}`), 0o600)
	_, err := requireSignIn(context.Background(), &rootFlags{catalogLab: "L"}, "run")
	if err == nil || !strings.Contains(err.Error(), "older version of astrona") || !strings.Contains(err.Error(), `run "astrona login" first`) {
		t.Errorf("old credentials: %v", err)
	}
	os.WriteFile(store.Path, []byte(`{"site":"`+srv.URL+`","issuer":"x","client_id":"astrona-cli","access_token":"a"}`), 0o600)
	out := captureStdout(t, func() { _ = logout(client, store) })
	if !strings.Contains(out, "older astrona") {
		t.Errorf("logout of old credentials = %q", out)
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
	err := login(context.Background(), account.NewClient(), store, srv.URL, testDevice)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || !strings.Contains(err.Error(), "astrona login --site http://localhost:3000") {
		t.Errorf("err = %v", err)
	}
}

// compactJSON is out (one JSON document) without whitespace.
func compactJSON(t *testing.T, out string) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, []byte(out)); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, out)
	}
	return b.String()
}
