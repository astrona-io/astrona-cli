package workspace

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/account"
	"astrona/internal/config"
	"astrona/internal/labops"
	"astrona/internal/trust"
)

type catalogTransport func(*http.Request) (*http.Response, error)

func (f catalogTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBrowserTrustOfficialPin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	sha := git("rev-parse", "HEAD")
	src := labops.LabSource{Git: "https://github.com/astrona-io/ATS014.git", Catalog: "ATS014/section-010/module-01/lab-01"}
	cfg := &config.LabConfig{}
	oldDeps := labops.AccountDeps
	t.Cleanup(func() { labops.AccountDeps = oldDeps })
	for _, tc := range []struct {
		name, pin   string
		dirty       bool
		unavailable bool
		wantOK      bool
	}{
		{name: "published clean commit", pin: sha, wantOK: true},
		{name: "different published commit", pin: strings.Repeat("0", 40)},
		{name: "catalog unavailable", unavailable: true},
		{name: "dirty checkout", pin: sha, dirty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.dirty {
				if err := os.WriteFile(filepath.Join(repo, "unapproved.sh"), []byte("echo changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			lookups := 0
			client := &account.Client{HTTP: &http.Client{Transport: catalogTransport(func(r *http.Request) (*http.Response, error) {
				lookups++
				if r.URL.String() != account.DefaultSite+"/api/labs" || r.Header.Get("Authorization") != "" {
					t.Errorf("unexpected catalog request: %s", r.URL)
				}
				if tc.unavailable {
					return nil, fmt.Errorf("catalog unavailable")
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"trainings":[{"repo":"ats014","sourceSha":%q}]}`, tc.pin))), Header: http.Header{}}, nil
			})}}
			labops.AccountDeps = func() (*account.Client, account.Store, string, error) {
				return client, account.Store{}, account.DefaultSite, nil
			}
			err := labops.RequireTrust(src, cfg, repo, browserTrustOptions(io.Discard))
			if (err == nil) != tc.wantOK {
				t.Fatalf("RequireTrust error = %v, want success %t", err, tc.wantOK)
			}
			if lookups != 1 {
				t.Fatalf("official pin lookups = %d, want 1", lookups)
			}
			ts, err := labops.TrustSource(src, cfg, repo)
			if err != nil {
				t.Fatal(err)
			}
			status, _, err := trust.Check(*ts)
			if err != nil || status != trust.New {
				t.Fatalf("official check persisted consent: status %v, error %v", status, err)
			}
		})
	}
}
