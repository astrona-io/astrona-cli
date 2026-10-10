package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"astrona/internal/trust"
	"astrona/internal/version"

	"github.com/mattn/go-isatty"
)

func fakeBinary(t *testing.T, dir, name string, executable bool) {
	t.Helper()
	mode := os.FileMode(0644)
	if executable {
		mode = 0755
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledVersionsAndNewestAllowed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".astrona", "bin")
	os.MkdirAll(bin, 0755)
	other := t.TempDir()
	t.Setenv("PATH", other)

	fakeBinary(t, bin, "astrona-0.2.1", true)
	fakeBinary(t, bin, "astrona-0.1.9", true)
	fakeBinary(t, bin, "astrona-0.2.5", false)  // not executable
	fakeBinary(t, bin, "astrona-latest", true)  // not a version
	fakeBinary(t, other, "astrona-0.3.0", true) // on PATH only: never a hand-over target

	var got []string
	for _, iv := range installedVersions() {
		got = append(got, iv.v.String()+"@"+filepath.Base(filepath.Dir(iv.path)))
	}
	if strings.Join(got, " ") != "0.2.1@bin 0.1.9@bin" {
		t.Errorf("installed = %v", got)
	}

	c, _ := version.ParseConstraint("<=0.2.1")
	if iv, ok := newestAllowed(c, installedVersions()); !ok || iv.v.String() != "0.2.1" {
		t.Errorf("newest allowed for <=0.2.1 = %+v, %v", iv, ok)
	}
	// 0.1.9 is installed, but below the hand-over floor.
	c, _ = version.ParseConstraint("<0.2.0")
	if iv, ok := newestAllowed(c, handoverCandidates()); ok {
		t.Errorf("hand-over candidate below the floor: %+v", iv)
	}
	c, _ = version.ParseConstraint(">=1.0.0")
	if _, ok := newestAllowed(c, installedVersions()); ok {
		t.Error("nothing should match >=1.0.0")
	}
}

func TestHandoverArgs(t *testing.T) {
	f := &rootFlags{configPath: "/labs/web", fileName: "config.yaml", labArg: "./labs/web"}
	got := strings.Join(handoverArgs([]string{"run", "./labs/web", "--verbose"}, f), " ")
	if got != "run --verbose -c /labs/web -f config.yaml" {
		t.Errorf("lab argument = %s", got)
	}
	g := &rootFlags{configPath: "labs/net", fileName: "config.yaml", gitURL: "https://github.com/org/labs", gitRef: "v2"}
	got = strings.Join(handoverArgs([]string{"submit"}, g), " ")
	if got != "submit -c labs/net -f config.yaml --git https://github.com/org/labs --git-ref v2" {
		t.Errorf("git lab = %s", got)
	}
	// The user's own lab flags are replaced by the resolved ones — a stale
	// --git-ref (a catalog lab resets it) doesn't survive.
	h := &rootFlags{configPath: "sections/x/lab-01", fileName: "config.yaml", gitURL: "https://github.com/astrona-io/ATS014.git", labArg: "ATS014/x/lab-01"}
	got = strings.Join(handoverArgs([]string{"run", "--git-ref", "old", "ATS014/x/lab-01", "-c=elsewhere", "-fother.yaml", "--git=https://example.com/r", "--config", "y", "--yes"}, h), " ")
	if got != "run --yes -c sections/x/lab-01 -f config.yaml --git https://github.com/astrona-io/ATS014.git" {
		t.Errorf("user lab flags = %s", got)
	}
	// After "--" everything is the command's own.
	got = strings.Join(handoverArgs([]string{"res", "run", "x", "--", "-c", "1"}, &rootFlags{configPath: ".", fileName: "config.yaml"}), " ")
	if got != "res run x -- -c 1 -c . -f config.yaml" {
		t.Errorf("after -- = %s", got)
	}
}

// targetFlags really asks the other binary for its command's --help.
func TestTargetFlagsRunsHelp(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "astrona-0.2.0")
	script := "#!/bin/sh\n[ \"$1 $2\" = \"run --help\" ] || exit 3\nprintf 'Flags:\\n  -h, --help   help\\n      --parallel int   x\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	known, err := targetFlags(bin, []string{"run"})
	if err != nil || !known["parallel"] || !known["h"] || known["keep-context"] {
		t.Errorf("known = %v, %v", known, err)
	}
	if _, err := targetFlags(bin, []string{"submit"}); err == nil {
		t.Error("a failing --help should be an error")
	}
}

// A flag the handed-to version doesn't have fails before the hand-over,
// naming it and the version.
func TestCheckHandoverFlags(t *testing.T) {
	help := `Start a lab

Usage:
  astrona run [lab] [flags]

Flags:
  -h, --help              help for run
      --parallel int      linked clusters at once

Global Flags:
  -c, --config string     Path or URL
  -f, --file string       Configuration file name override (default "config.yaml")
      --git string        Git repository URL
      --git-ref string    Git branch
`
	known := parseHelpFlags(help)
	for _, f := range []string{"help", "h", "parallel", "config", "c", "file", "f", "git", "git-ref"} {
		if !known[f] {
			t.Errorf("%s not found in help", f)
		}
	}
	defer func(old func(string, []string) (map[string]bool, error)) { targetFlags = old }(targetFlags)
	var asked []string
	targetFlags = func(_ string, cmdPath []string) (map[string]bool, error) { asked = cmdPath; return known, nil }
	v020, _ := version.Parse("0.2.0")
	target := installedVersion{v: v020, path: "/bin/astrona-0.2.0"}
	c, _ := version.ParseConstraint("<0.3.0")
	flags := &rootFlags{cmdPath: []string{"run"}}

	if err := checkHandoverFlags(target, c, []string{"run", "--parallel=2", "-c", "x", "-f", "config.yaml"}, flags); err != nil {
		t.Errorf("known flags: %v", err)
	}
	if strings.Join(asked, " ") != "run" {
		t.Errorf("asked about %v", asked)
	}
	for _, bad := range []string{"--keep-context", "-y", "--yes"} {
		err := checkHandoverFlags(target, c, []string{"run", bad, "-c", "x"}, flags)
		if err == nil || !strings.Contains(err.Error(), "astrona 0.2.0") || !strings.Contains(err.Error(), "no "+bad) || !strings.Contains(err.Error(), "<0.3.0") {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if err := checkHandoverFlags(target, c, []string{"res", "run", "x", "--", "--anything"}, flags); err != nil {
		t.Errorf("after --: %v", err)
	}
	// A version that can't be asked decides itself.
	targetFlags = func(string, []string) (map[string]bool, error) { return nil, errors.New("no") }
	if err := checkHandoverFlags(target, c, []string{"run", "--keep-context"}, flags); err != nil {
		t.Errorf("unaskable: %v", err)
	}
}

// allow is a trust check that approves.
func allow() error { return nil }

func TestEnsureLabVersion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	releaseTags = func() ([]string, error) { return []string{"v0.3.0", "v0.2.1", "v0.2.0"}, nil }
	defer func() { releaseTags = githubReleaseTags }()
	old := Version
	defer func() { Version = old }()

	Version = "v0.2.2"
	if err := ensureLabVersion("", &rootFlags{}, allow); err != nil {
		t.Errorf("no constraint: %v", err)
	}
	if err := ensureLabVersion(">=0.2.0", &rootFlags{}, allow); err != nil {
		t.Errorf("allowed: %v", err)
	}
	stdinIsTerminal = func() bool { return false }
	defer func() { stdinIsTerminal = func() bool { return isatty.IsTerminal(os.Stdin.Fd()) } }()
	err := ensureLabVersion("<=0.2.1", &rootFlags{}, allow)
	if err == nil || !strings.Contains(err.Error(), "astrona versions install 0.2.1") || !strings.Contains(err.Error(), "--install-version") {
		t.Errorf("not allowed, none installed, no terminal = %v — must say what to install", err)
	}
	if err := ensureLabVersion("~0.2", &rootFlags{}, allow); err == nil {
		t.Error("bad constraint accepted")
	}

	t.Setenv(dispatchedEnv, "0.3.0")
	if err := ensureLabVersion("<=0.2.1", &rootFlags{}, allow); err == nil || !strings.Contains(err.Error(), "handed it to") {
		t.Errorf("second hand-over must stop: %v", err)
	}

	Version = "developer"
	if err := ensureLabVersion("<=0.2.1", &rootFlags{}, allow); err != nil {
		t.Errorf("developer build should not be blocked: %v", err)
	}
}

func TestNewerRelease(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.2", "v0.2.1", true},
		{"v0.2.1", "v0.2.2", false},
		{"v0.2.1", "v0.2.1", false},
		{"v0.3.0", "v0.3.0-rc1", true},
		{"v0.2.1", "developer", false},
		{"", "v0.2.1", false},
	} {
		if got := newerRelease(c.latest, c.current); got != c.want {
			t.Errorf("newerRelease(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}

// When no allowed version is installed: --install-version installs it, a
// yes at the terminal installs it, a no doesn't — and then the command is
// handed over to it, without our own --install-version.
func TestOfferInstall(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv(dispatchedEnv, "")
	old, origInstall, origExec, origTerm := Version, installVersion, execHandover, stdinIsTerminal
	Version = "v0.2.2"
	releaseTags = func() ([]string, error) { return []string{"v0.3.0", "v0.2.1", "v0.2.0"}, nil }
	var installed []string
	installVersion = func(v version.V) (string, error) {
		installed = append(installed, v.String())
		return "/fake/astrona-" + v.String(), nil
	}
	errHandedOver := errors.New("handed over")
	var handedArgs []string
	execHandover = func(path string, argv, env []string) error {
		handedArgs = argv
		return errHandedOver
	}
	origArgs := os.Args
	defer func() {
		Version, releaseTags, installVersion, execHandover, stdinIsTerminal = old, githubReleaseTags, origInstall, origExec, origTerm
		promptIn, os.Args = os.Stdin, origArgs
	}()

	// --install-version: no question asked, even without a terminal.
	stdinIsTerminal = func() bool { return false }
	os.Args = []string{"astrona", "run", "--install-version"}
	f := &rootFlags{configPath: "/labs/old", fileName: "config.yaml", installVersion: true}
	if err := ensureLabVersion("<=0.2.1", f, allow); !errors.Is(err, errHandedOver) {
		t.Fatalf("--install-version = %v", err)
	}
	if strings.Join(installed, ",") != "0.2.1" {
		t.Errorf("installed = %v — want the newest allowed release", installed)
	}
	if strings.Contains(strings.Join(handedArgs, " "), "--install-version") || handedArgs[0] != "/fake/astrona-0.2.1" {
		t.Errorf("handed over as %v — the older version doesn't know --install-version", handedArgs)
	}

	// At a terminal: "y" installs, "n" doesn't.
	stdinIsTerminal = func() bool { return true }
	installed = nil
	os.Args = []string{"astrona", "run"}
	promptIn = strings.NewReader("y\n")
	if err := ensureLabVersion("<=0.2.1", &rootFlags{configPath: "/labs/old", fileName: "config.yaml"}, allow); !errors.Is(err, errHandedOver) || len(installed) != 1 {
		t.Errorf("yes = %v, installed %v", err, installed)
	}
	installed = nil
	promptIn = strings.NewReader("n\n")
	err := ensureLabVersion("<=0.2.1", &rootFlags{configPath: "/labs/old", fileName: "config.yaml"}, allow)
	if err == nil || errors.Is(err, errHandedOver) || len(installed) != 0 || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("no = %v, installed %v", err, installed)
	}

	// No published release fits: say so, install nothing.
	installed = nil
	promptIn = strings.NewReader("y\n")
	if err := ensureLabVersion(">=9.0.0", &rootFlags{}, allow); err == nil || len(installed) != 0 || !strings.Contains(err.Error(), "no published release fits") {
		t.Errorf("nothing fits = %v, installed %v", err, installed)
	}
}

// handoverFakes stubs releases, installs and the hand-over itself for one
// test: this is 0.2.2, the published releases are tags, HOME is empty and
// there's no terminal. It returns what got installed, the argv handed
// over to, and the error the fake hand-over returns.
func handoverFakes(t *testing.T, tags ...string) (installed, handed *[]string, errHandedOver error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv(dispatchedEnv, "")
	old, origInstall, origExec, origTerm, origArgs := Version, installVersion, execHandover, stdinIsTerminal, os.Args
	t.Cleanup(func() {
		Version, releaseTags, installVersion, execHandover, stdinIsTerminal, os.Args = old, githubReleaseTags, origInstall, origExec, origTerm, origArgs
	})
	Version = "v0.2.2"
	os.Args = []string{"astrona", "run"}
	stdinIsTerminal = func() bool { return false }
	releaseTags = func() ([]string, error) { return tags, nil }
	installed, handed = &[]string{}, &[]string{}
	installVersion = func(v version.V) (string, error) {
		*installed = append(*installed, v.String())
		return "/fake/astrona-" + v.String(), nil
	}
	errHandedOver = errors.New("handed over")
	execHandover = func(path string, argv, env []string) error {
		*handed = argv
		return errHandedOver
	}
	return installed, handed, errHandedOver
}

func installFake(t *testing.T, name string) {
	t.Helper()
	bin := filepath.Join(os.Getenv("HOME"), ".astrona", "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	fakeBinary(t, bin, name, true)
}

// A lab may not pick an astrona from before trust prompts: not an
// installed one, not one installed for it — even with --install-version.
func TestHandoverFloor(t *testing.T) {
	installed, handed, errHandedOver := handoverFakes(t, "v0.2.1", "v0.2.0", "v0.1.9")
	installFake(t, "astrona-0.1.9")

	f := &rootFlags{configPath: "/labs/old", fileName: "config.yaml", installVersion: true}
	err := ensureLabVersion("<0.2.0", f, allow)
	if err == nil || errors.Is(err, errHandedOver) || !strings.Contains(err.Error(), "older than "+minHandoverVersion) {
		t.Errorf("below the floor = %v", err)
	}
	if len(*installed) != 0 || len(*handed) != 0 {
		t.Errorf("below the floor: installed %v, handed to %v", *installed, *handed)
	}

	// A constraint that also fits a version above the floor gets that one
	// installed, not the older one that's already there.
	if err := ensureLabVersion("<=0.2.1", f, allow); !errors.Is(err, errHandedOver) {
		t.Fatalf("<=0.2.1 = %v", err)
	}
	if strings.Join(*installed, ",") != "0.2.1" || (*handed)[0] != "/fake/astrona-0.2.1" {
		t.Errorf("installed %v, handed to %v — want 0.2.1", *installed, *handed)
	}
}

// The trust check runs before anything is installed or handed over, and a
// refusal stops both.
func TestHandoverTrustFirst(t *testing.T) {
	installed, handed, errHandedOver := handoverFakes(t, "v0.2.1")
	var order []string
	fakeInstall := installVersion
	installVersion = func(v version.V) (string, error) {
		order = append(order, "install")
		return fakeInstall(v)
	}
	f := &rootFlags{configPath: "/labs/old", fileName: "config.yaml", installVersion: true}

	errRefused := errors.New("not trusted")
	refuse := func() error { order = append(order, "trust"); return errRefused }
	if err := ensureLabVersion("<=0.2.1", f, refuse); !errors.Is(err, errRefused) {
		t.Errorf("refused = %v", err)
	}
	if len(*installed) != 0 || len(*handed) != 0 {
		t.Errorf("refused, yet installed %v, handed to %v", *installed, *handed)
	}

	order = nil
	approve := func() error { order = append(order, "trust"); return nil }
	if err := ensureLabVersion("<=0.2.1", f, approve); !errors.Is(err, errHandedOver) {
		t.Fatalf("approved = %v", err)
	}
	if strings.Join(order, ",") != "trust,install" {
		t.Errorf("order = %v — trust must come first", order)
	}

	// Already installed: still checked before the hand-over.
	installFake(t, "astrona-0.2.1")
	*handed = nil
	if err := ensureLabVersion("<=0.2.1", f, refuse); !errors.Is(err, errRefused) || len(*handed) != 0 {
		t.Errorf("installed, refused = %v, handed to %v", err, *handed)
	}
}

// End to end through LoadLabForCommand: a remote lab that wants another
// astrona is trust-checked before the hand-over — whether or not this
// version can parse its config — and the approval is stored, so the
// version it's handed to doesn't ask again.
func TestLoadLabForCommandTrustsBeforeHandover(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"parses", "astronaVersion: \"<=0.2.1\"\nmetadata:\n  name: old\n"},
		{"doesn't parse", "astronaVersion: \"<=0.2.1\"\nmetadata: not-a-map\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, handed, errHandedOver := handoverFakes(t)
			installFake(t, "astrona-0.2.1")
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			origTransport := http.DefaultTransport // trusts the test server's certificate
			http.DefaultTransport = srv.Client().Transport
			defer func() { http.DefaultTransport = origTransport }()
			url := srv.URL + "/config.yaml"

			// go test's stdin isn't a terminal: refused, naming --trust.
			_, _, _, err := LoadLabForCommand(&rootFlags{configPath: url, fileName: "config.yaml"})
			if err == nil || errors.Is(err, errHandedOver) || !strings.Contains(err.Error(), "--trust") || len(*handed) != 0 {
				t.Fatalf("untrusted = %v, handed to %v", err, *handed)
			}

			_, _, _, err = LoadLabForCommand(&rootFlags{configPath: url, fileName: "config.yaml", trust: true})
			if !errors.Is(err, errHandedOver) {
				t.Fatalf("--trust = %v", err)
			}
			sum := sha256.Sum256([]byte(tc.body))
			status, _, err := trust.Check(trust.Source{Kind: "url", Location: url, Pin: "sha256:" + hex.EncodeToString(sum[:])})
			if err != nil || status != trust.Trusted {
				t.Errorf("approval not stored for the version handed to: %v, %v", status, err)
			}
		})
	}
}
