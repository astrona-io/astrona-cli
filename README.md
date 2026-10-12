# astrona-cli

[![Liberapay](https://img.shields.io/badge/Liberapay-Support_Astrona.io-F6C915?logo=liberapay&logoColor=black&style=for-the-badge)](https://liberapay.com/Astrona.io)

Astrona spins up local Kubernetes labs with [kind](https://kind.sigs.k8s.io/), backed by whichever container runtime you have — Docker or Podman. Everything is driven by a single YAML config per lab.

## Features

- **Local kind clusters** — auto-detects Docker or Podman on your `PATH` and creates/deletes the kind cluster accordingly, no manual provider flags needed.
- **Config from anywhere** — point `--config`/`-c` at a local directory, a local file, or an `http(s)://` URL; `--file`/`-f` overrides the config file name (default `config.yaml`).
- **Bootstrap** — run init scripts (local file or downloaded from a URL) and apply Kubernetes manifests when the lab starts.
- **Testing stage** — a CI-only stage that applies a reference solution (scripts and/or manifests) to drive the cluster into the "lab completed" state, so validation has something to check.
- **Proctor grading** — a student never grades their own work: `astrona submit` hands the cluster to the Proctor, which runs declarative checks (`resourceExists`, `podReady`, `command`) and/or one or more custom pass/fail scripts (`script`/`scripts`) and returns a PASS/FAIL verdict — the same component `astrona test` uses in CI to prove a lab's reference solution actually passes. Output is pytest/robot-style: a PASS/FAIL line with timing per check, then a summary line. `--junit-xml=<path>` on `submit`/`test` additionally writes a JUnit XML report for CI systems (GitHub Actions, GitLab CI, Jenkins) to render natively.
- **Teardown** — cleanup scripts run before the cluster is deleted, with an optional `keepCluster` flag to leave the cluster running for debugging.
- **Doc references** — `metadata.docs` points at prerequisites, a formal exam-style question, a softer case-study version, and a step-by-step answer guide, so a marketplace listing or terminal UI always knows which file is canonical instead of guessing.

## Commands

Flat, podman-`run`-style verbs at the root — no noun to namespace under. `--config`/`-c`, `--file`/`-f`, `--git`, and `--git-ref` are persistent flags on the root command (shared by every subcommand), not repeated per-command.

`astrona --help` lists every command, grouped by what you're doing:

- **Get started** — `setup`, `login`, `whoami`, `logout`
- **Take a lab** — `labs`, `use`, `run`, `docs`, `shell`, `resource`, `ssh`, `submit`, `status`, `progress`, `reset`, `destroy`
- **Manage running labs** — `list`, `stop`, `start`, `port-forward`, `net`, `kubeconfig`
- **Write labs** — `init`, `content`, `validate`, `test`, `bundle`
- **Troubleshoot** — `doctor`, `check`, `diagnose`, `logs`, `images`
- **astrona itself** — `terms`, `settings`, `data`, `versions`, `upgrade`

On first use, Astrona asks you to accept its CLI usage terms and data-loss risk
notice. Read them with `astrona terms`. A changed notice in a later CLI release
requires acceptance again. CI commands need an explicit `--accept-terms` flag;
`--yes` and `--trust` do not accept terms. See [terms acceptance](docs/getting-started/installation.md#cli-usage-terms).

`astrona data` shows local file counts and sizes. `astrona data clean` previews
cache cleanup; `astrona data reset` previews resetting saved preferences and the
selected lab. Both require `--yes` to delete anything. Sign-in details, lab state,
results and student files are kept. See [local data management](docs/getting-started/installation.md#local-data-and-cache-cleanup).

Save CLI preferences with `astrona settings set keep-context true` or
`astrona settings set parallel 2`. `astrona settings` lists supported keys;
explicit command-line flags override saved values. Preferences are stored in
`~/.astrona/settings.json`. See [persistent settings](docs/getting-started/installation.md#persistent-cli-settings).

`astrona settings view` explains current values, defaults and pending changes.
The first operational command asks you to review settings, and asks again when
a release changes their definitions. Use `astrona settings review` to choose
values explicitly. CI needs a matching, reviewed settings ID; accepting usage
terms does not approve settings. See [settings review](docs/getting-started/installation.md#review-settings-after-an-upgrade).

Disposable caches default to 30 days and 50 GB combined. Change the limits with
`astrona settings set cache-retention-days 60` and
`astrona settings set cache-max-size-gb 100`; size `0` means unlimited.
Preview policy cleanup with `astrona data clean --policy`. Automatic cleanup
runs after operational commands and defers while saved labs or another CLI
command may need the files. See [cache limits](docs/getting-started/installation.md#automatic-cache-limits).

`astrona submit` and `astrona test` exit 2 when the lab was graded and didn't pass, 1 on any other error. `astrona check` exits non-zero on any ✗ — a missing required dependency (`kind`, `docker`/`podman`, `kubectl`), a container engine that isn't running or is too small, or a lab (from `-c`/`--git`, `astrona use`, or `./config.yaml`) that can't be loaded or won't fit; ⚠ warnings — such as the optional qemu toolchain or `git` missing — don't fail it.

Every command, flag and example is in the [CLI reference](https://cli.astrona.io/reference/cli/astrona/).

## Lab config

```yaml
metadata:
  name: "k8s-basics-01"
  docs:
    prerequisites: "docs/prerequisites.md"    # knowledge/tooling needed before attempting
    question: "docs/exam-question.md"     # formal, self-contained task statement
    caseStudy: "docs/case-study.md"           # softer, hint-driven version of the same task
    solution: "docs/step-by-step-guide.md"       # full walkthrough with the answer

bootstrap:
  init:
    - name: "echo"
      type: "file"
      source: "hello.sh"
  manifests: []

testing:
  manifests:
    - name: "solution"
      type: "folder"
      source: "solution"

validation:
  checks:
    - name: "lab-ns namespace exists"
      type: "resourceExists"
      resource: "namespace/lab-ns"
  script:                          # optional: goes beyond existence checks, e.g. verifying actual content
    type: "file"
    source: "validate.sh"          # exit 0 = pass, non-zero = fail
  scripts:                         # optional, additive: more validation scripts, run in order after `script`
    - name: "check-network"
      type: "file"
      source: "validate-network.sh"
    - name: "check-storage"
      type: "file"
      source: "validate-storage.sh"

teardown:
  init:
    - name: "dump-logs"
      type: "file"
      source: "teardown/dump-logs.sh"
  keepCluster: false
```

See `examples/k8s-basics-01/` for a complete working example.

## Installation

### Homebrew (macOS & Linux)

```sh
brew install astrona-io/tap/astrona
```

Homebrew also installs Kind, kubectl, Git, ORAS, QEMU and `cdrtools` for the lab runtimes. Install and start Docker or Podman separately.

Upgrade with `brew upgrade astrona` — or `astrona upgrade`, which runs it for you.

Or download a pre-compiled binary of `astrona-cli` directly from GitHub Releases:

### Linux packages (APT, DNF & Snap)

For Debian/Ubuntu, download and verify the latest stable package before installing:

```sh
(
  set -eu
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "Unsupported CPU architecture" >&2; exit 1 ;;
  esac
  release_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/astrona-io/astrona-cli/releases/latest)"
  tag="${release_url##*/}"
  package="astrona-${tag#v}-linux-$arch.deb"
  base="https://github.com/astrona-io/astrona-cli/releases/download/$tag"
  download_dir="$(mktemp -d)"
  cd "$download_dir"
  curl -fSL "$base/$package" -o "$package"
  curl -fsSL "$base/SHA256SUMS" -o SHA256SUMS
  awk -v file="$package" '$2 == file { print }' SHA256SUMS | sha256sum -c -
  sudo apt-get install "./$package"
  astrona --version
)
```

See the [APT, DNF and Snap installation instructions](https://cli.astrona.io/getting-started/installation/#apt-debian-ubuntu) for complete download-and-install commands for each format. Packages are available once a release includes this feature.

Packages include Kind, kubectl and ORAS; Git and certificates are installed as dependencies or bundled in the Snap. Install and start Docker or Podman separately. These are local release packages; hosted APT/DNF repositories and Snap Store publication are separate setup steps. See [installation details](https://cli.astrona.io/getting-started/installation/) for upgrades and optional VM dependencies.

### Quick Install (macOS & Linux)

Run the following command in your terminal to automatically detect your OS and architecture, download the latest binary, and install it to `~/.local/bin/astrona`:

```sh
mkdir -p ~/.local/bin && \
OS=$(uname -s | tr '[:upper:]' '[:lower:]') && \
ARCH=$(uname -m) && \
[ "$ARCH" = "x86_64" ] && ARCH="amd64" || true && \
[ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ] && ARCH="arm64" || true && \
curl -L -o ~/.local/bin/astrona "https://github.com/astrona-io/astrona-cli/releases/latest/download/astrona-${OS}-${ARCH}" && \
chmod +x ~/.local/bin/astrona
```

*(Note: Make sure `~/.local/bin` is added to your shell's `PATH` configuration.)*

### Manual Installation

1. Go to the [GitHub Releases](https://github.com/astrona-io/astrona-cli/releases) page.
2. Download the binary matching your platform and architecture:
   - macOS (Apple Silicon): `astrona-darwin-arm64`
   - macOS (Intel): `astrona-darwin-amd64`
   - Linux (ARM64): `astrona-linux-arm64`
   - Linux (AMD64): `astrona-linux-amd64`
3. Make the downloaded binary executable:
   ```sh
   chmod +x astrona-<os>-<arch>
   ```
4. Move it into your `PATH` (renaming it to `astrona`):
   ```sh
   mv astrona-<os>-<arch> ~/.local/bin/astrona
   ```

## Developing

A [`Justfile`](./Justfile) wraps the common local dev commands (requires [`just`](https://github.com/casey/just)):

| Recipe | What it does |
|---|---|
| `just build` | `go build -o astrona .` |
| `just install` | Build, then move the binary onto `PATH` (`~/.local/bin` by default, override with `ASTRONA_INSTALL_DIR`) |
| `just check` | `fmt` + `vet` + `test` — run before committing |

Run `just` with no arguments to list every recipe.

## Requirements

- [`kind`](https://kind.sigs.k8s.io/)
- [`kubectl`](https://kubernetes.io/docs/tasks/tools/#kubectl)
- Docker or Podman
- [`just`](https://github.com/casey/just) (optional, for the `Justfile` recipes above)

## Support

Astrona is free and built on donations and community support. If it's useful to you, consider [donating via Liberapay](https://liberapay.com/Astrona.io) — thank you to everyone who contributes, sponsors, and helps keep it going.

## License

Apache License 2.0 — see [LICENSE](./LICENSE).
