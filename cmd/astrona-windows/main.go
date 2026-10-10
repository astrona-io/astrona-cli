// Command astrona-windows is astrona.exe: the Windows front door to the
// Astrona CLI.
//
// Labs need Linux (kind, bash bootstrap scripts, Docker), so astrona does not
// run natively on Windows. astrona.exe instead:
//
//   - `astrona setup` prepares WSL 2 with Ubuntu, checks Docker Desktop's WSL
//     integration, installs the real astrona inside Ubuntu, runs its own setup
//     there, and puts astrona.exe on the user's PATH;
//   - every other command is forwarded unchanged into Ubuntu, so a student
//     types `astrona run …` in PowerShell exactly as the docs say.
//
// It is built from a separate package so it compiles without the Unix-only
// parts of the CLI (process sessions, signals, qemu).
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"astrona/internal/version"
)

// Version is set at build time: -ldflags "-X main.Version=v0.4.0".
var Version = "dev"

const defaultDistro = "Ubuntu"

// releasePath is the GitHub release download path for the Linux astrona
// matching this launcher: its own tag when it was built from one, and
// latest/download otherwise (a "dev" or unparseable version) — pinned is
// false then, so setup can say so.
func releasePath(v string) (path string, pinned bool) {
	if _, err := version.Parse(v); err == nil && strings.HasPrefix(v, "v") && strings.TrimSpace(v) == v {
		return "download/" + v, true
	}
	return "latest/download", false
}

// installInWSL installs the Linux astrona into ~/.local/bin inside the
// distribution: the documented quick install, pinned to this launcher's
// release when it has one. The binary is checked against that release's
// SHA256SUMS before it is made executable or run; a missing entry or a
// mismatch stops the install with nothing put in place. It runs as a fixed
// script — no user input is interpolated into it.
func installInWSL(v string) string {
	release, _ := releasePath(v)
	return `set -eu
ARCH=$(uname -m)
case "$ARCH" in x86_64) ARCH=amd64;; aarch64|arm64) ARCH=arm64;; *) echo "astrona: unsupported CPU architecture $ARCH" >&2; exit 1;; esac
BIN="astrona-linux-$ARCH"
BASE="https://github.com/astrona-io/astrona-cli/releases/` + release + `"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
curl -fsSL --proto '=https' --proto-redir '=https' -o "$TMP/$BIN" "$BASE/$BIN"
curl -fsSL --proto '=https' --proto-redir '=https' -o "$TMP/SHA256SUMS" "$BASE/SHA256SUMS"
SUM=$(awk -v f="$BIN" '$2 == f' "$TMP/SHA256SUMS")
if [ -z "$SUM" ]; then echo "astrona: SHA256SUMS has no entry for $BIN — not installing it" >&2; exit 1; fi
if ! (cd "$TMP" && printf '%s\n' "$SUM" | sha256sum --check --strict -); then echo "astrona: $BIN does not match the release's SHA256SUMS — not installing it" >&2; exit 1; fi
mkdir -p "$HOME/.local/bin"
install -m 0755 "$TMP/$BIN" "$HOME/.local/bin/astrona"
grep -q '.local/bin' "$HOME/.profile" 2>/dev/null || echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$HOME/.profile"
"$HOME/.local/bin/astrona" --version`
}

// inDistro is the wsl.exe argv that runs argv inside the distribution.
// --exec runs the program directly; with "--" wsl.exe would instead hand the
// rest of the command line to the distribution's default shell, which
// re-parses it — expanding $… and $(…), splitting on ; and | — so every
// argument could be lost or run as code.
func inDistro(distro string, argv ...string) []string {
	return append([]string{"-d", distro, "--exec"}, argv...)
}

// wsl is how the launcher talks to WSL; swapped out in tests.
type wsl interface {
	// output runs wsl.exe with args and returns its stdout (decoded) and error.
	output(args ...string) (string, error)
	// attach runs wsl.exe with args on the student's terminal.
	attach(args ...string) error
}

type realWSL struct{}

func (realWSL) output(args ...string) (string, error) {
	out, err := exec.Command("wsl.exe", args...).Output()
	return decodeWSL(out), err
}

func (realWSL) attach(args ...string) error {
	c := exec.Command("wsl.exe", args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// decodeWSL turns wsl.exe output into text. wsl.exe's own messages (like
// `wsl -l -q`) are UTF-16LE; commands run inside the distribution print
// UTF-8. Detect by the NUL bytes UTF-16 leaves in ASCII text.
func decodeWSL(b []byte) string {
	if len(b) >= 2 && (b[0] == 0xFF && b[1] == 0xFE) {
		b = b[2:]
	} else if !hasNULs(b) {
		return string(b)
	}
	if len(b)%2 == 1 {
		b = b[:len(b)-1]
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func hasNULs(b []byte) bool {
	for _, c := range b {
		if c == 0 {
			return true
		}
	}
	return false
}

// distroList parses `wsl -l -q`: one name per line, possibly with a "*" or
// whitespace around it.
func distroList(out string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func distroName() string {
	if d := strings.TrimSpace(os.Getenv("ASTRONA_WSL_DISTRO")); d != "" {
		return d
	}
	return defaultDistro
}

// forwardArgs is the wsl.exe argv that runs `astrona <args…>` inside the
// distribution. A login shell picks up ~/.local/bin from ~/.profile; the
// student's arguments travel as positional parameters ("$@"), never as part
// of the script, and --exec (see inDistro) keeps any shell from re-parsing
// them on the way in, so nothing they type is interpolated.
func forwardArgs(distro string, args []string) []string {
	return append(inDistro(distro, "bash", "-lc", `exec astrona "$@"`, "astrona"), args...)
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"--help"}
	}
	switch args[0] {
	case "setup":
		if err := setup(realWSL{}, os.Stdin, os.Stdout, args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		return
	case "--launcher-version":
		fmt.Println("astrona.exe (Windows launcher)", Version)
		return
	}
	os.Exit(forward(realWSL{}, args))
}

// forward runs the command inside WSL and returns its exit code, so
// `astrona submit` still exits 2 for "graded, didn't pass".
func forward(w wsl, args []string) int {
	distro := distroName()
	if err := w.attach(forwardArgs(distro, args)...); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() == 127 {
				fmt.Fprintln(os.Stderr, "astrona is not installed inside WSL yet. Run: astrona setup")
			}
			return exit.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "Could not start WSL (%v).\nRun astrona setup to get WSL 2 and %s ready.\n", err, distro)
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- setup

type options struct {
	yes, dryRun bool
}

func parseSetupFlags(args []string) (options, error) {
	var o options
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			o.yes = true
		case "--dry-run":
			o.dryRun = true
		case "-h", "--help":
			return o, errHelp
		default:
			return o, fmt.Errorf("unknown flag %q (setup takes --yes and --dry-run)", a)
		}
	}
	return o, nil
}

var errHelp = errors.New("help")

const setupHelp = `astrona setup (Windows) — get this PC ready for Astrona labs.

Labs run in Linux, so astrona uses WSL 2 (Linux inside Windows). Setup:
  1. turns on WSL 2 and installs Ubuntu (asks for admin; may need a restart)
  2. checks Docker Desktop is installed with WSL integration on for Ubuntu
  3. installs astrona inside Ubuntu and runs its setup (kind, kubectl)
  4. puts astrona.exe on your PATH, so "astrona …" works from any PowerShell

Run it again after a restart or a manual step; finished steps are skipped.
Needs Windows 10 22H2 or Windows 11, and virtualization turned on in BIOS/UEFI.

Flags:
  -y, --yes     approve every step without asking
      --dry-run show what setup would do, change nothing`

func setup(w wsl, in io.Reader, out io.Writer, args []string) error {
	opts, err := parseSetupFlags(args)
	if errors.Is(err, errHelp) {
		fmt.Fprintln(out, setupHelp)
		return nil
	}
	if err != nil {
		return err
	}
	distro := distroName()
	reader := bufio.NewReader(in)
	ask := func(q string) bool {
		if opts.yes {
			return true
		}
		fmt.Fprintf(out, "   %s [y/N] ", q)
		line, _ := reader.ReadString('\n')
		a := strings.ToLower(strings.TrimSpace(line))
		return a == "y" || a == "yes"
	}
	step := func(n int, title, why string) {
		fmt.Fprintf(out, "\n%d. %s\n   %s\n", n, title, why)
	}

	// 1. WSL itself.
	step(1, "WSL 2", "Linux inside Windows. Labs need Linux to run.")
	if _, err := w.output("--status"); err != nil {
		fmt.Fprintln(out, "   WSL is not installed.")
		fmt.Fprintln(out, "   $ wsl --install --no-distribution")
		if opts.dryRun {
			return nil
		}
		if !ask("Install WSL now? Windows will ask for administrator permission.") {
			return errors.New("WSL is needed — run astrona setup again when you are ready")
		}
		if err := w.attach("--install", "--no-distribution"); err != nil {
			return fmt.Errorf("installing WSL failed (%w). If it mentions virtualization, turn it on in your BIOS/UEFI: https://astrona.io/labs/troubleshooting#wsl", err)
		}
		fmt.Fprintln(out, "\n   WSL is installed. Restart Windows, then run astrona setup again.")
		return nil
	}
	fmt.Fprintln(out, "   ✓ installed")

	// 2. The distribution.
	step(2, distro, "The Linux system astrona and your labs run in.")
	list, _ := w.output("-l", "-q")
	if !contains(distroList(list), distro) {
		fmt.Fprintf(out, "   %s is not installed.\n   $ wsl --install -d %s\n", distro, distro)
		if opts.dryRun {
			return nil
		}
		if !ask("Install " + distro + " now? A window opens and asks you to choose a Linux username and password.") {
			return errors.New(distro + " is needed — run astrona setup again when you are ready")
		}
		if err := w.attach("--install", "-d", distro); err != nil {
			return fmt.Errorf("installing %s failed: %w", distro, err)
		}
		fmt.Fprintf(out, "\n   When %s has finished and you have chosen a username, run astrona setup again.\n", distro)
		return nil
	}
	if _, err := w.output(inDistro(distro, "true")...); err != nil {
		return fmt.Errorf("%s is installed but does not start. Open it once from the Start menu to finish its first-time setup, then run astrona setup again", distro)
	}
	fmt.Fprintln(out, "   ✓ installed")

	// 3. Docker, through Docker Desktop's WSL integration.
	step(3, "Docker", "kind runs each Kubernetes node as a container. Docker Desktop provides Docker inside "+distro+".")
	if _, err := w.output(inDistro(distro, "docker", "info", "--format", "{{.ServerVersion}}")...); err != nil {
		fmt.Fprintf(out, "   → do this yourself: install Docker Desktop (https://www.docker.com/products/docker-desktop/), start it,\n"+
			"     then Settings → Resources → WSL integration → turn on %s. Guide: https://docs.docker.com/desktop/features/wsl/\n", distro)
		if opts.dryRun {
			return nil
		}
		return errors.New("Docker is not reachable inside " + distro + " yet — run astrona setup again once Docker Desktop is running with WSL integration on")
	}
	fmt.Fprintln(out, "   ✓ Docker answers inside "+distro)

	// 4. astrona inside the distribution.
	step(4, "astrona inside "+distro, "The real astrona, which builds and grades your labs.")
	if _, err := w.output(inDistro(distro, "bash", "-lc", "command -v astrona")...); err != nil {
		fmt.Fprintln(out, "   downloads astrona for Linux into ~/.local/bin inside "+distro+", checked against the release's SHA256SUMS")
		if _, pinned := releasePath(Version); !pinned {
			fmt.Fprintf(out, "   note: this astrona.exe has no release version (%q), so it installs the latest release instead of a matching one\n", Version)
		}
		if opts.dryRun {
			return nil
		}
		if !ask("Install it now?") {
			return errors.New("astrona inside " + distro + " is needed — run astrona setup again when you are ready")
		}
		if err := w.attach(inDistro(distro, "bash", "-lc", installInWSL(Version))...); err != nil {
			return fmt.Errorf("installing astrona inside %s failed: %w", distro, err)
		}
	} else {
		fmt.Fprintln(out, "   ✓ installed")
	}

	// 5. The Linux setup: kind and kubectl, then astrona check.
	step(5, "kind and kubectl", "Installed by astrona setup inside "+distro+" (it shows each step and asks).")
	if !opts.dryRun {
		innerArgs := []string{"setup"}
		if opts.yes {
			innerArgs = append(innerArgs, "--yes")
		}
		if err := w.attach(forwardArgs(distro, innerArgs)...); err != nil {
			return fmt.Errorf("astrona setup inside %s did not finish: %w", distro, err)
		}
	}

	// 6. astrona.exe on PATH.
	step(6, "astrona in PowerShell", "So typing astrona in any new PowerShell window runs your labs.")
	if opts.dryRun {
		fmt.Fprintln(out, "   copies astrona.exe to %LOCALAPPDATA%\\Programs\\astrona and adds it to your user PATH")
		return nil
	}
	if err := installSelfFn(out); err != nil {
		return err
	}
	fmt.Fprintln(out, "\nReady. Open a new PowerShell window and try: astrona labs")
	return nil
}

func contains(list []string, s string) bool {
	for _, item := range list {
		if strings.EqualFold(item, s) {
			return true
		}
	}
	return false
}

// installSelfFn is installSelf; a variable so tests skip touching PATH.
var installSelfFn = installSelf

// installSelf copies the running astrona.exe into a stable per-user folder and
// adds that folder to the user PATH (not the machine PATH, so no admin is
// needed). The PATH update goes through PowerShell's .NET API, which — unlike
// setx — does not truncate a long PATH.
func installSelf(out io.Writer) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return errors.New("LOCALAPPDATA is not set — copy astrona.exe somewhere on your PATH yourself")
	}
	dir := filepath.Join(local, "Programs", "astrona")
	dest := filepath.Join(dir, "astrona.exe")
	if !strings.EqualFold(filepath.Clean(self), filepath.Clean(dest)) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := copyFile(self, dest); err != nil {
			return fmt.Errorf("copying astrona.exe: %w", err)
		}
	}
	script := `$d = $env:ASTRONA_DIR; $p = [Environment]::GetEnvironmentVariable('Path','User'); ` +
		`if (-not (($p -split ';') -contains $d)) { [Environment]::SetEnvironmentVariable('Path', ($(if ($p) { "$p;$d" } else { $d })), 'User') }`
	ps := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	ps.Env = append(os.Environ(), "ASTRONA_DIR="+dir)
	if b, err := ps.CombinedOutput(); err != nil {
		return fmt.Errorf("adding %s to your PATH failed: %w %s", dir, err, strings.TrimSpace(string(b)))
	}
	fmt.Fprintf(out, "   ✓ %s is on your PATH\n", dir)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
