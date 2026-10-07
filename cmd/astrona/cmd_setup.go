package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// setupStep is one thing `astrona setup` would do: either commands it runs
// (after asking), or — when it can't safely do it for the student — a manual
// instruction it prints. download is set for the kind/kubectl fetch on Linux
// without Homebrew, which astrona does itself (checksum-verified).
type setupStep struct {
	title    string
	why      string
	commands [][]string
	download *toolDownload
	manual   string
	// waitForEngine: after the commands, poll until the container engine
	// answers (Docker Desktop and podman machines take a while to boot).
	waitForEngine bool
}

// setupEnv is everything setup's plan depends on, gathered once so the plan
// itself is a pure function (and testable without touching the machine).
type setupEnv struct {
	goos, goarch string
	hasBrew      bool
	missing      map[string]bool // depCheck names that are not found: "kind", "kubectl", "docker or podman"
	// Container engine state.
	hasDocker, hasPodman bool
	engineReachable      bool
	podmanMachineExists  bool
	podmanMachineRunning bool
	localBin             string // ~/.local/bin
	localBinOnPath       bool
}

func gatherSetupEnv() setupEnv {
	env := setupEnv{goos: goruntime.GOOS, goarch: goruntime.GOARCH, hasBrew: brewAvailable(), missing: map[string]bool{}}
	for _, c := range missingDeps(true) {
		env.missing[c.name] = true
	}
	_, dockerErr := exec.LookPath("docker")
	_, podmanErr := exec.LookPath("podman")
	env.hasDocker, env.hasPodman = dockerErr == nil, podmanErr == nil
	res, _ := checkEngine()
	env.engineReachable = len(res) > 0 && res[0].status != checkFail
	if env.hasPodman && env.goos == "darwin" {
		out, err := exec.Command("podman", "machine", "list", "--format", "{{.Running}}").Output()
		if err == nil {
			lines := strings.Fields(string(out))
			env.podmanMachineExists = len(lines) > 0
			for _, l := range lines {
				if l == "true" {
					env.podmanMachineRunning = true
				}
			}
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		env.localBin = filepath.Join(home, ".local", "bin")
		for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
			if filepath.Clean(dir) == env.localBin {
				env.localBinOnPath = true
			}
		}
	}
	return env
}

// planSetup decides what to do. Order matters: tools first, then the
// container engine (installing podman makes a machine possible), so a fresh
// Mac goes from nothing to ready in one run.
func planSetup(env setupEnv) []setupStep {
	var steps []setupStep
	needKind, needKubectl, needEngine := env.missing["kind"], env.missing["kubectl"], env.missing["docker or podman"]

	switch {
	case !needKind && !needKubectl && !needEngine:
		// Every tool is there.
	case env.hasBrew:
		var formulas []string
		if needKind {
			formulas = append(formulas, "kind")
		}
		if needKubectl {
			formulas = append(formulas, "kubernetes-cli")
		}
		// Podman from Homebrew is the easy engine on macOS. On Linux a
		// container engine belongs to the distribution (rootless setup,
		// systemd), so it stays a manual step below.
		if needEngine && env.goos == "darwin" {
			formulas = append(formulas, "podman")
		}
		if len(formulas) > 0 {
			steps = append(steps, setupStep{
				title:    "Install " + strings.Join(formulas, ", ") + " with Homebrew",
				why:      "kind creates the lab's Kubernetes cluster, kubectl talks to it, and Podman runs kind's containers.",
				commands: [][]string{append([]string{"brew", "install"}, formulas...)},
			})
		}
	case env.goos == "linux":
		for _, tool := range []string{"kind", "kubectl"} {
			if !env.missing[tool] {
				continue
			}
			steps = append(steps, setupStep{
				title:    fmt.Sprintf("Download %s into %s", tool, env.localBin),
				why:      map[string]string{"kind": "kind creates the lab's Kubernetes cluster.", "kubectl": "kubectl is how you (and the grader) talk to the cluster."}[tool],
				download: &toolDownload{tool: tool, goos: env.goos, goarch: env.goarch, dir: env.localBin},
			})
		}
	default:
		steps = append(steps, setupStep{
			title:  "Install Homebrew",
			why:    "Homebrew installs kind, kubectl and Podman for you. astrona setup uses it on macOS.",
			manual: "Follow https://brew.sh (one command in the Terminal), then run astrona setup again.",
		})
		return steps
	}

	if needEngine && env.goos == "linux" {
		steps = append(steps, setupStep{
			title: "Install a container engine (Docker or Podman)",
			why:   "kind runs each Kubernetes node as a container, so it needs Docker or Podman.",
			manual: "On Ubuntu/Debian: sudo apt-get update && sudo apt-get install -y docker.io && sudo usermod -aG docker $USER (then log out and back in).\n" +
				"Inside WSL on Windows, install Docker Desktop on Windows instead and turn on WSL integration: https://docs.docker.com/desktop/features/wsl/\n" +
				"Other distributions: https://docs.docker.com/engine/install/ or https://podman.io/docs/installation",
		})
	}

	// The engine is installed (or about to be) but not answering.
	engineWillBePodman := env.hasPodman || (needEngine && env.goos == "darwin" && env.hasBrew)
	if !env.engineReachable {
		switch {
		case env.goos == "darwin" && engineWillBePodman && !env.hasDocker:
			var cmds [][]string
			if !env.podmanMachineExists {
				cmds = append(cmds, []string{"podman", "machine", "init", "--cpus", "4", "--memory", "8192"})
			}
			if !env.podmanMachineRunning {
				cmds = append(cmds, []string{"podman", "machine", "start"})
			}
			if len(cmds) > 0 {
				steps = append(steps, setupStep{
					title:         "Create and start the Podman machine",
					why:           "On macOS, Podman runs containers in a small Linux VM. 4 CPUs and 8 GB lets most labs fit; change it later with podman machine set.",
					commands:      cmds,
					waitForEngine: true,
				})
			}
		case env.goos == "darwin" && env.hasDocker:
			steps = append(steps, setupStep{
				title:         "Start Docker Desktop",
				why:           "Docker is installed but not running, so kind cannot create containers.",
				commands:      [][]string{{"open", "-a", "Docker"}},
				waitForEngine: true,
			})
		case env.goos == "linux" && (env.hasDocker || env.hasPodman):
			steps = append(steps, setupStep{
				title:  "Start your container engine",
				why:    "It is installed but not answering.",
				manual: "Docker: sudo systemctl start docker (or start Docker Desktop). Podman: check `podman info`.",
			})
		}
	}

	if env.goos == "linux" && !env.hasBrew && (needKind || needKubectl) && !env.localBinOnPath {
		steps = append(steps, setupStep{
			title:  "Put " + env.localBin + " on your PATH",
			why:    "That is where kind and kubectl were downloaded.",
			manual: `Add this line to ~/.bashrc (or ~/.zshrc), then open a new terminal: export PATH="$HOME/.local/bin:$PATH"`,
		})
	}
	return steps
}

// setupRunner executes a plan; swapped out in tests.
type setupRunner struct {
	in       io.Reader
	out      io.Writer
	yes      bool
	dryRun   bool
	run      func(argv []string) error
	download func(context.Context, *toolDownload) (string, error)
	waitUp   func(timeout time.Duration) bool
}

func (r setupRunner) execute(steps []setupStep) (manualLeft int, err error) {
	for i, step := range steps {
		fmt.Fprintf(r.out, "\n%d. %s\n   %s\n", i+1, colorize(ansiBold, step.title), step.why)
		if step.manual != "" {
			fmt.Fprintf(r.out, "   %s %s\n", colorize(ansiYellow, "→ do this yourself:"), strings.ReplaceAll(step.manual, "\n", "\n     "))
			manualLeft++
			continue
		}
		for _, argv := range step.commands {
			fmt.Fprintf(r.out, "   $ %s\n", strings.Join(argv, " "))
		}
		if step.download != nil {
			fmt.Fprintf(r.out, "   downloads %s and checks its SHA-256 against the published checksum\n", step.download.tool)
		}
		if r.dryRun {
			continue
		}
		if !r.yes && !confirmYes(r.in, promptOut, "   Do this now?") {
			fmt.Fprintln(r.out, "   skipped")
			manualLeft++
			continue
		}
		for _, argv := range step.commands {
			if err := r.run(argv); err != nil {
				return manualLeft, fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
			}
		}
		if step.download != nil {
			path, err := r.download(context.Background(), step.download)
			if err != nil {
				return manualLeft, err
			}
			fmt.Fprintf(r.out, "   %s installed %s\n", colorize(ansiGreen, "✓"), path)
		}
		if step.waitForEngine {
			fmt.Fprint(r.out, "   waiting for the container engine to answer…")
			if r.waitUp(3 * time.Minute) {
				fmt.Fprintln(r.out, " "+colorize(ansiGreen, "ready"))
			} else {
				fmt.Fprintln(r.out, " still not answering — give it a minute, then run astrona check")
			}
		}
	}
	return manualLeft, nil
}

func newSetupCmd() *cobra.Command {
	var yes, dryRun bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install and start what this machine needs to run labs (asks first)",
		Long: "Get this machine ready for labs in one go. astrona setup looks at what `astrona check` " +
			"would report, shows each step it would take, and asks before doing it:\n\n" +
			"  • macOS: installs kind, kubectl and Podman with Homebrew, then creates and starts the Podman\n" +
			"    machine (or starts Docker Desktop if that is what you have).\n" +
			"  • Linux: installs kind and kubectl with Homebrew, or downloads them into ~/.local/bin and\n" +
			"    verifies their published SHA-256 checksums. A container engine belongs to your\n" +
			"    distribution, so setup tells you the command instead of running it with sudo.\n" +
			"  • Windows: use astrona.exe, whose setup prepares WSL 2 and installs astrona inside it.\n\n" +
			"It finishes with `astrona check`. Nothing is installed without a yes, unless you pass --yes.",
		Example: "  astrona setup\n  astrona setup --dry-run\n  astrona setup --yes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if goruntime.GOOS != "darwin" && goruntime.GOOS != "linux" {
				return fmt.Errorf("astrona setup supports macOS and Linux; on Windows use astrona.exe setup")
			}
			if !yes && !dryRun && !isatty.IsTerminal(os.Stdin.Fd()) {
				return errors.New("astrona setup asks before each step and there is no terminal to ask on — pass --yes to approve every step, or --dry-run to see the plan")
			}
			env := gatherSetupEnv()
			steps := planSetup(env)
			if len(steps) == 0 {
				fmt.Println("Everything astrona needs is installed and running.")
			} else {
				fmt.Printf("astrona setup will take %d step(s)%s:\n", len(steps), map[bool]string{true: " (dry run — nothing will change)", false: ""}[dryRun])
				runner := setupRunner{
					in: os.Stdin, out: os.Stdout, yes: yes, dryRun: dryRun,
					run:      runVisible,
					download: installToolDownload,
					waitUp:   waitForEngine,
				}
				manual, err := runner.execute(steps)
				if err != nil {
					return err
				}
				if dryRun {
					return nil
				}
				if manual > 0 {
					fmt.Printf("\n%d step(s) are left for you (see → above). Run astrona setup again when they are done.\n", manual)
				}
			}
			fmt.Println("\nChecking the result (astrona check):")
			self, err := os.Executable()
			if err != nil {
				return err
			}
			check := exec.Command(self, "check")
			check.Stdout, check.Stderr = os.Stdout, os.Stderr
			if err := check.Run(); err != nil {
				return fmt.Errorf("astrona check still reports problems — see above, or the troubleshooting guide: https://astrona.io/labs/troubleshooting")
			}
			fmt.Println("\nReady. Browse labs with astrona labs, or pick one on https://astrona.io/labs")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Approve every step without asking")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what setup would do, change nothing")
	return cmd
}

// runVisible runs argv with the terminal attached (brew and podman print
// their own progress, and may ask questions of their own).
func runVisible(argv []string) error {
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// waitForEngine polls the container engine until it answers or timeout.
func waitForEngine(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if res, _ := checkEngine(); len(res) > 0 && res[0].status != checkFail {
			return true
		}
		time.Sleep(3 * time.Second)
	}
	return false
}

// ---------------------------------------------------------------- downloads

// toolDownload is kind or kubectl for one platform, fetched from its
// project's official release location over HTTPS.
type toolDownload struct {
	tool, goos, goarch, dir string
}

// maxToolBytes caps a download (kubectl is ~60 MB, kind ~10 MB).
const maxToolBytes = 200 << 20

var setupHTTP = &http.Client{Timeout: 5 * time.Minute}

// toolURLs returns the binary URL and the URL of its published SHA-256.
// A variable so tests can point it at a local server.
var toolURLs = func(ctx context.Context, d toolDownload) (binURL, sumURL string, err error) {
	switch d.tool {
	case "kind":
		base := fmt.Sprintf("https://github.com/kubernetes-sigs/kind/releases/latest/download/kind-%s-%s", d.goos, d.goarch)
		return base, base + ".sha256sum", nil
	case "kubectl":
		version, err := fetchSmall(ctx, "https://dl.k8s.io/release/stable.txt")
		if err != nil {
			return "", "", fmt.Errorf("looking up the current kubectl version: %w", err)
		}
		version = strings.TrimSpace(version)
		if !strings.HasPrefix(version, "v") || strings.ContainsAny(version, "/ \n") {
			return "", "", fmt.Errorf("unexpected kubectl version %q", version)
		}
		base := fmt.Sprintf("https://dl.k8s.io/release/%s/bin/%s/%s/kubectl", version, d.goos, d.goarch)
		return base, base + ".sha256", nil
	}
	return "", "", fmt.Errorf("unknown tool %q", d.tool)
}

func fetchSmall(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := setupHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return string(b), err
}

// parseChecksum takes the first field of a sha256 file ("<hex>" or
// "<hex>  <name>") and checks it is 64 hex digits.
func parseChecksum(s string) (string, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", errors.New("empty checksum file")
	}
	sum := strings.ToLower(fields[0])
	if b, err := hex.DecodeString(sum); err != nil || len(b) != sha256.Size {
		return "", fmt.Errorf("checksum file does not start with a SHA-256: %q", fields[0])
	}
	return sum, nil
}

// installToolDownload downloads the tool, verifies it against the published
// checksum, and only then moves it into place (0755). A mismatch leaves
// nothing behind.
func installToolDownload(ctx context.Context, d *toolDownload) (string, error) {
	binURL, sumURL, err := toolURLs(ctx, *d)
	if err != nil {
		return "", err
	}
	sumText, err := fetchSmall(ctx, sumURL)
	if err != nil {
		return "", fmt.Errorf("downloading %s checksum: %w", d.tool, err)
	}
	want, err := parseChecksum(sumText)
	if err != nil {
		return "", fmt.Errorf("%s checksum: %w", d.tool, err)
	}
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(d.dir, "."+d.tool+"-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, binURL, nil)
	if err != nil {
		tmp.Close()
		return "", err
	}
	resp, err := setupHTTP.Do(req)
	if err != nil {
		tmp.Close()
		return "", fmt.Errorf("downloading %s: %w", d.tool, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		return "", fmt.Errorf("downloading %s: %s", d.tool, resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxToolBytes+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", d.tool, err)
	}
	if n > maxToolBytes {
		return "", fmt.Errorf("%s download is larger than %d MB — refusing it", d.tool, maxToolBytes>>20)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("%s checksum mismatch: got %s, published %s — nothing was installed", d.tool, got, want)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(d.dir, d.tool)
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return "", err
	}
	return dest, nil
}
