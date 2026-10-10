package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf16le(s string, bom bool) []byte {
	var b []byte
	if bom {
		b = append(b, 0xFF, 0xFE)
	}
	for _, r := range utf16.Encode([]rune(s)) {
		b = binary.LittleEndian.AppendUint16(b, r)
	}
	return b
}

func TestDecodeWSL(t *testing.T) {
	for _, tc := range []struct {
		in   []byte
		want string
	}{
		{utf16le("Ubuntu\r\ndocker-desktop\r\n", false), "Ubuntu\r\ndocker-desktop\r\n"},
		{utf16le("Ubuntu\r\n", true), "Ubuntu\r\n"},
		{[]byte("27.3.1\n"), "27.3.1\n"},
	} {
		if got := decodeWSL(tc.in); got != tc.want {
			t.Errorf("decodeWSL(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDistroList(t *testing.T) {
	got := distroList("Ubuntu\r\n* docker-desktop\r\n\r\n")
	if len(got) != 2 || got[0] != "Ubuntu" || got[1] != "docker-desktop" {
		t.Fatalf("distroList = %q", got)
	}
}

// The student's arguments are positional parameters, never part of the
// bash script — `astrona run "a; rm -rf ~"` must not run rm. And wsl.exe gets
// --exec, not "--": with "--" it hands the command line to the
// distribution's default shell, which re-parses it (expanding "$@" to
// nothing and running $(…)).
func TestForwardArgsNeverInterpolates(t *testing.T) {
	args := forwardArgs("Ubuntu", []string{"run", "a; rm -rf ~", "$(whoami)", "", "it's"})
	want := []string{"-d", "Ubuntu", "--exec", "bash", "-lc", `exec astrona "$@"`, "astrona", "run", "a; rm -rf ~", "$(whoami)", "", "it's"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("forwardArgs = %q", args)
	}
}

// Every command setup runs inside the distribution goes through --exec.
func TestSetupNeverUsesDefaultShell(t *testing.T) {
	outs := readyOutputs()
	delete(outs, "-d Ubuntu --exec bash -lc command -v astrona")
	f := &recordingWSL{fakeWSL: fakeWSL{outputs: outs}}
	old := installSelfFn
	installSelfFn = func(io.Writer) error { return nil }
	defer func() { installSelfFn = old }()
	if err := setup(f, strings.NewReader(""), io.Discard, []string{"--yes"}); err != nil {
		t.Fatal(err)
	}
	for _, call := range append(f.calls, f.attached...) {
		for i, a := range call {
			if a == "--" {
				t.Errorf("wsl.exe %q passes the command to the default shell", call)
			}
			if a == "-d" && (i+2 >= len(call) || call[i+2] != "--exec") {
				t.Errorf("wsl.exe %q runs in the distribution without --exec", call)
			}
		}
	}
}

type recordingWSL struct {
	fakeWSL
	calls [][]string
}

func (r *recordingWSL) output(args ...string) (string, error) {
	r.calls = append(r.calls, args)
	return r.fakeWSL.output(args...)
}

func TestReleasePath(t *testing.T) {
	for _, tc := range []struct {
		v      string
		want   string
		pinned bool
	}{
		{"v0.4.0", "download/v0.4.0", true},
		{"v1.2.3-rc1", "download/v1.2.3-rc1", true},
		{"dev", "latest/download", false},
		{"0.4.0", "latest/download", false},
		{"v1; rm -rf ~", "latest/download", false},
		{"v1$(id)", "latest/download", false},
		{" v0.4.0", "latest/download", false},
	} {
		got, pinned := releasePath(tc.v)
		if got != tc.want || pinned != tc.pinned {
			t.Errorf("releasePath(%q) = %q, %v; want %q, %v", tc.v, got, pinned, tc.want, tc.pinned)
		}
	}
}

func TestInstallInWSLPinsReleaseOnlyForCleanVersions(t *testing.T) {
	if s := installInWSL("v0.4.0"); !strings.Contains(s, `releases/download/v0.4.0"`) {
		t.Errorf("pinned release missing: %s", s)
	}
	for _, v := range []string{"dev", "v1; rm -rf ~", "v1$(id)"} {
		if s := installInWSL(v); !strings.Contains(s, `releases/latest/download"`) || strings.Contains(s, "rm -rf ~") || strings.Contains(s, "$(id)") {
			t.Errorf("version %q leaked into the script: %s", v, s)
		}
	}
}

// runInstallScript runs installInWSL's script with bash against a fake
// release: curl and uname are stubs, files come from release, HOME is a temp
// dir. It returns HOME, the URLs fetched, the output and the script's error.
func runInstallScript(t *testing.T, v string, release map[string]string) (home string, urls []string, out string, err error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the install script runs inside Linux")
	}
	for _, tool := range []string{"bash", "sha256sum", "awk", "install", "mktemp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available: %v", tool, err)
		}
	}
	dir := t.TempDir()
	home, stubs, files := filepath.Join(dir, "home"), filepath.Join(dir, "bin"), filepath.Join(dir, "release")
	for _, d := range []string{home, stubs, files} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range release {
		if err := os.WriteFile(filepath.Join(files, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(dir, "urls")
	curl := `#!/bin/sh
o=""; url=""
while [ $# -gt 0 ]; do case "$1" in -o) o="$2"; shift 2;; --proto|--proto-redir) shift 2;; -*) shift;; *) url="$1"; shift;; esac; done
echo "$url" >> "` + log + `"
f="` + files + `/${url##*/}"
[ -f "$f" ] || exit 22
cp "$f" "$o"
`
	if err := os.WriteFile(filepath.Join(stubs, "curl"), []byte(curl), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stubs, "uname"), []byte("#!/bin/sh\necho x86_64\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", installInWSL(v))
	cmd.Env = []string{"HOME=" + home, "PATH=" + stubs + string(os.PathListSeparator) + os.Getenv("PATH")}
	b, err := cmd.CombinedOutput()
	logged, _ := os.ReadFile(log)
	return home, strings.Fields(string(logged)), string(b), err
}

const fakeAstrona = "#!/bin/sh\necho astrona v0.4.0\n"

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestInstallScriptVerifiesChecksum(t *testing.T) {
	good := sha256Hex(fakeAstrona) + "  astrona-linux-amd64\n" + sha256Hex("other") + "  astrona-linux-arm64\n"
	for _, tc := range []struct {
		name    string
		sums    string
		install bool
	}{
		{"match", good, true},
		{"mismatch", sha256Hex("tampered") + "  astrona-linux-amd64\n", false},
		{"no entry", sha256Hex(fakeAstrona) + "  astrona-linux-arm64\n", false},
		{"no SHA256SUMS", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := map[string]string{"astrona-linux-amd64": fakeAstrona}
			if tc.sums != "" {
				release["SHA256SUMS"] = tc.sums
			}
			home, urls, out, err := runInstallScript(t, "v0.4.0", release)
			_, statErr := os.Stat(filepath.Join(home, ".local", "bin", "astrona"))
			if tc.install {
				if err != nil || statErr != nil || !strings.Contains(out, "astrona v0.4.0") {
					t.Fatalf("err=%v stat=%v out=%s", err, statErr, out)
				}
			} else if err == nil || statErr == nil {
				t.Fatalf("installed an unverified binary: err=%v out=%s", err, out)
			}
			for _, u := range urls {
				if !strings.HasPrefix(u, "https://github.com/astrona-io/astrona-cli/releases/download/v0.4.0/") {
					t.Errorf("fetched %s, not from the pinned release", u)
				}
			}
		})
	}
}

// A dev launcher installs latest — and still verifies against latest's sums.
func TestInstallScriptDevBuildUsesLatestAndVerifies(t *testing.T) {
	_, urls, out, err := runInstallScript(t, "dev", map[string]string{
		"astrona-linux-amd64": fakeAstrona,
		"SHA256SUMS":          sha256Hex("tampered") + "  astrona-linux-amd64\n",
	})
	if err == nil {
		t.Fatalf("dev build installed without verification: %s", out)
	}
	want := []string{
		"https://github.com/astrona-io/astrona-cli/releases/latest/download/astrona-linux-amd64",
		"https://github.com/astrona-io/astrona-cli/releases/latest/download/SHA256SUMS",
	}
	if strings.Join(urls, " ") != strings.Join(want, " ") {
		t.Errorf("urls = %q", urls)
	}
}

func TestSetupSaysWhenNotPinned(t *testing.T) {
	outs := readyOutputs()
	delete(outs, "-d Ubuntu --exec bash -lc command -v astrona")
	var out bytes.Buffer
	if err := setup(&fakeWSL{outputs: outs}, strings.NewReader(""), &out, []string{"--dry-run"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `no release version ("dev")`) {
		t.Errorf("no notice for a dev launcher: %s", out.String())
	}
}

// fakeWSL answers output() from a table (missing key = error) and records attach() calls.
type fakeWSL struct {
	outputs   map[string]string
	attached  [][]string
	attachErr error
}

func (f *fakeWSL) output(args ...string) (string, error) {
	if out, ok := f.outputs[strings.Join(args, " ")]; ok {
		return out, nil
	}
	return "", errors.New("exit status 1")
}

func (f *fakeWSL) attach(args ...string) error {
	f.attached = append(f.attached, args)
	return f.attachErr
}

func readyOutputs() map[string]string {
	return map[string]string{
		"--status":              "Default Version: 2",
		"-l -q":                 "Ubuntu\r\n",
		"-d Ubuntu --exec true": "",
		"-d Ubuntu --exec docker info --format {{.ServerVersion}}": "27.3.1",
		"-d Ubuntu --exec bash -lc command -v astrona":             "/home/s/.local/bin/astrona",
	}
}

func TestSetupInstallsWSLWhenMissing(t *testing.T) {
	f := &fakeWSL{outputs: map[string]string{}}
	var out bytes.Buffer
	if err := setup(f, strings.NewReader("y\n"), &out, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.attached) != 1 || strings.Join(f.attached[0], " ") != "--install --no-distribution" {
		t.Fatalf("attached = %q", f.attached)
	}
	if !strings.Contains(out.String(), "Restart Windows") {
		t.Errorf("no restart instruction: %s", out.String())
	}
}

func TestSetupDeclinedChangesNothing(t *testing.T) {
	f := &fakeWSL{outputs: map[string]string{}}
	if err := setup(f, strings.NewReader("n\n"), io.Discard, nil); err == nil || len(f.attached) != 0 {
		t.Fatalf("err=%v attached=%q", err, f.attached)
	}
}

func TestSetupDryRunAttachesNothing(t *testing.T) {
	outs := readyOutputs()
	delete(outs, "-d Ubuntu --exec bash -lc command -v astrona")
	f := &fakeWSL{outputs: outs}
	if err := setup(f, strings.NewReader(""), io.Discard, []string{"--dry-run"}); err != nil || len(f.attached) != 0 {
		t.Fatalf("err=%v attached=%q", err, f.attached)
	}
}

func TestSetupStopsAtDockerWithInstructions(t *testing.T) {
	outs := readyOutputs()
	delete(outs, "-d Ubuntu --exec docker info --format {{.ServerVersion}}")
	f := &fakeWSL{outputs: outs}
	var out bytes.Buffer
	err := setup(f, strings.NewReader(""), &out, nil)
	if err == nil || !strings.Contains(out.String(), "WSL integration") || len(f.attached) != 0 {
		t.Fatalf("err=%v attached=%q out=%s", err, f.attached, out.String())
	}
}

func TestSetupFullRunInstallsAstronaRunsInnerSetupAndPath(t *testing.T) {
	outs := readyOutputs()
	delete(outs, "-d Ubuntu --exec bash -lc command -v astrona")
	f := &fakeWSL{outputs: outs}
	pathed := false
	old := installSelfFn
	installSelfFn = func(io.Writer) error { pathed = true; return nil }
	defer func() { installSelfFn = old }()

	if err := setup(f, strings.NewReader(""), io.Discard, []string{"--yes"}); err != nil {
		t.Fatal(err)
	}
	if len(f.attached) != 2 {
		t.Fatalf("want install + inner setup, got %q", f.attached)
	}
	if !strings.Contains(f.attached[0][len(f.attached[0])-1], "astrona-linux-") {
		t.Errorf("first attach is not the install script: %q", f.attached[0])
	}
	if got := strings.Join(f.attached[1], " "); !strings.HasSuffix(got, "astrona setup --yes") {
		t.Errorf("inner setup = %q", got)
	}
	if !pathed {
		t.Error("astrona.exe not put on PATH")
	}
}

func TestSetupRejectsUnknownFlags(t *testing.T) {
	if err := setup(&fakeWSL{}, strings.NewReader(""), io.Discard, []string{"--frobnicate"}); err == nil {
		t.Fatal("unknown flag accepted")
	}
}
