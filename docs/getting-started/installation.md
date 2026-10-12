# Installation

## Requirements

Astrona shells out to a few external tools — `astrona check` (below) verifies all of these for you:

| Tool | Required | Needed for |
|---|---|---|
| [`kind`](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) | Yes | creates and deletes the local Kubernetes cluster |
| Docker or Podman | Yes | container runtime `kind` runs on (either works) |
| [`kubectl`](https://kubernetes.io/docs/tasks/tools/#kubectl) | Yes | applies manifests and runs the Proctor's declarative checks |
| `git` | Optional | only for `--git` (cloning/pulling a lab config from a repo) |
| `qemu-system-x86_64` / `qemu-system-aarch64`, `qemu-img`, `ssh`, `ssh-keygen` | Optional | only for `runtime.type: qemu` labs |
| `oras` | Optional | only for a qemu lab pulling its base image from an OCI registry (`image.type: oci`) |
| `mkisofs` / `genisoimage` / `xorriso` / `hdiutil` (any one) | Optional | only for `runtime.type: qemu` (builds the cloud-init seed image) |

## The short way: `astrona setup`

After installing astrona (below), one command gets the rest of the machine ready:

```sh
astrona setup             # shows each step and asks before doing it
astrona setup --dry-run   # only show the plan
astrona setup --yes       # approve every step (no questions)
```

It looks at what [`astrona check`](#verify) would report and fixes what it can:

| On | What setup does |
|---|---|
| macOS | `brew install` whatever is missing of kind, kubectl and Podman, then `podman machine init --cpus 4 --memory 8192` and `podman machine start` (or starts Docker Desktop if that is what you have), and waits until the engine answers |
| macOS without Homebrew | tells you to install [Homebrew](https://brew.sh) first, then run setup again |
| Linux with Homebrew | `brew install` kind and kubectl |
| Linux without Homebrew | downloads kind (GitHub release) and kubectl (`dl.k8s.io`) into `~/.local/bin`, verified against their published SHA-256 before anything is moved into place |
| Linux, no container engine | prints the command to install Docker for your distribution — a container engine needs `sudo` and distribution-specific setup, so setup never runs that for you |

It ends by running `astrona check`. Without a terminal to ask on, setup refuses to change anything unless you pass `--yes`.

### Podman is installed but a lab will not start on macOS

Kind labs, including playgrounds, need a reachable container engine. A failed
`podman info` check can mean the Podman machine is stopped, but it can also mean
the connection is misconfigured. Run these commands to see which:

```sh
podman machine list
podman info
```

If your machine is stopped, start it with `podman machine start` (add its name
if you use a non-default machine). If no machine exists, run `astrona setup` to
review and approve the initialization steps. Setup retries failed health checks
while waiting for the engine to start.

If the machine is already running, inspect the error from `podman info` and
your connections with `podman system connection list`. Do not delete or recreate
the machine to troubleshoot: it may contain other containers and volumes.

Once `podman info` succeeds, run `astrona check` and retry your lab command.
The signed-out warning is separate: guest runs still need a working engine,
and signing in does not repair a container connection.

## Windows (preview)

Labs need Linux, so on Windows astrona runs inside [WSL 2](https://learn.microsoft.com/windows/wsl/) (Linux inside Windows). `astrona.exe` is a small launcher that sets that up and then forwards every command into it — you keep typing `astrona …` in PowerShell.

Requirements: Windows 10 22H2 or Windows 11, virtualization turned on in your BIOS/UEFI, and [Docker Desktop](https://www.docker.com/products/docker-desktop/).

In PowerShell:

```powershell
irm https://github.com/astrona-io/astrona-cli/releases/latest/download/astrona-windows-amd64.exe -OutFile $env:LOCALAPPDATA\astrona.exe
& $env:LOCALAPPDATA\astrona.exe setup
```

(`astrona-windows-arm64.exe` on ARM PCs.) `astrona setup` on Windows, one step at a time — run it again after a restart or a manual step, and finished steps are skipped:

1. **WSL 2** — `wsl --install --no-distribution` if WSL is missing (Windows asks for admin; restart afterwards).
2. **Ubuntu** — `wsl --install -d Ubuntu`; a window asks you to choose a Linux username and password. Set `ASTRONA_WSL_DISTRO` to use another distribution.
3. **Docker** — checks `docker info` works inside Ubuntu. If not, install Docker Desktop, start it, and turn on *Settings → Resources → WSL integration* for Ubuntu ([guide](https://docs.docker.com/desktop/features/wsl/)).
4. **astrona inside Ubuntu** — the Linux astrona, into `~/.local/bin` (same release as the launcher; a development build of `astrona.exe` says so and installs the latest release). The download is checked against that release's `SHA256SUMS` before it is made executable; if the checksum is missing or does not match, nothing is installed.
5. **kind and kubectl** — runs `astrona setup` inside Ubuntu.
6. **PATH** — copies `astrona.exe` to `%LOCALAPPDATA%\Programs\astrona` and adds that folder to your *user* PATH (no admin).

After that, any command — `astrona labs`, `astrona run …`, `astrona submit …` — runs inside Ubuntu, with the same output and exit codes (`astrona submit` still exits 2 for "graded, didn't pass"). Your arguments are passed through as they are, never re-parsed by a shell (the launcher uses `wsl --exec`, which skips the distribution's default shell). `astrona --launcher-version` prints the launcher's own version.

## Homebrew (macOS & Linux)

```sh
brew install astrona-io/tap/astrona
```

The [tap](https://github.com/astrona-io/homebrew-tap) is updated by every release, pinned to the same binaries and SHA-256 values as the GitHub release. CI installs the formula with real Homebrew on macOS and Linux for every change that could affect it, and again from the tap right after each release. Upgrade with `brew upgrade astrona`, or `astrona upgrade` — on a Homebrew install it runs `brew upgrade astrona` for you instead of downloading the binary itself.

What the Homebrew install gives you on top of the binary:

- **Lab tools are installed automatically**: `kind`, `kubectl` (`kubernetes-cli`), `git` for catalog/repository labs, `oras` for OCI VM images, `qemu` for VM labs, and `cdrtools` (`mkisofs`) for cloud-init seed images. Homebrew resolves and installs their dependencies too. Docker or Podman you install and start yourself — e.g. `brew install podman && podman machine init && podman machine start`, or Docker Desktop.
- **Shell completion** for bash, zsh and fish is set up — no `astrona completion` step (see [Shell completion](#shell-completion)).
- **Man pages**: `man astrona`, `man astrona-run`, … one per command.
- **No Gatekeeper prompt** on macOS: a binary downloaded in a browser gets the quarantine flag and macOS may refuse to run it; one installed by Homebrew doesn't.

Two things to know:

- An older `astrona` earlier on your `PATH` (e.g. `~/.local/bin/astrona` from the quick install) hides the Homebrew one — `which -a astrona` shows the order; remove the old one.
- `brew uninstall astrona` removes the program, not `~/.astrona` (lab state, results, approvals, installed versions). Destroy running labs first (`astrona destroy <lab>`); delete `~/.astrona` yourself if you want it gone.

## APT (Debian & Ubuntu)

Download the latest stable `.deb`, verify its SHA-256 checksum, and install it. This detects `amd64` (Intel/AMD) or `arm64` (ARM) automatically. You need `curl`, `sha256sum`, and permission to use `sudo`.

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

The commands below for DNF and Snap use the same download and verification process. Each resolves a single release tag so the package and its checksums come from the same release. These packages become available after the first release containing the Linux packaging feature; a missing asset stops the commands before installation. Downloaded files remain in the temporary directory; the subshell keeps your current directory unchanged.

APT installs Git and CA certificates automatically. The package includes pinned, checksum-verified **Kind, kubectl and ORAS**, installed privately under `/usr/lib/astrona/tools`. Astrona and its lab scripts use those tools without replacing tools you already have on your shell's PATH. `astrona shell` also inherits them.

Install and start **Docker or Podman** separately. For optional VM labs on Debian/Ubuntu:

```sh
sudo apt-get install qemu-system-x86 qemu-system-arm qemu-utils genisoimage openssh-client
```

## DNF (Fedora & Red Hat compatible distributions)

Download, verify and install the latest stable `.rpm` for your CPU:

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
  package="astrona-${tag#v}-linux-$arch.rpm"
  base="https://github.com/astrona-io/astrona-cli/releases/download/$tag"
  download_dir="$(mktemp -d)"
  cd "$download_dir"
  curl -fSL "$base/$package" -o "$package"
  curl -fsSL "$base/SHA256SUMS" -o SHA256SUMS
  awk -v file="$package" '$2 == file { print }' SHA256SUMS | sha256sum -c -
  sudo dnf install "./$package"
  astrona --version
)
```

DNF resolves Git and CA certificate dependencies. The RPM includes the same private Kind, kubectl and ORAS tools as the DEB. Install and start Docker or Podman separately. QEMU, SSH and an ISO builder are optional VM dependencies; their package names and availability depend on your distribution and enabled repositories. CI checks installation on Fedora and AlmaLinux 9; a Red Hat subscription and enabled repositories may be needed on RHEL.

These are local release packages. A hosted APT/DNF repository has not been configured, so `apt-get install astrona` or `dnf install astrona` without the downloaded file will not find Astrona. Run the download-and-install block again to install the latest stable release, or install a newer release file manually. Remove with `sudo apt-get remove astrona` or `sudo dnf remove astrona`.

## Snap (Ubuntu)

Download, verify and install the latest stable `.snap` for your CPU. Snap must already be installed on your system:

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
  package="astrona-${tag#v}-linux-$arch.snap"
  base="https://github.com/astrona-io/astrona-cli/releases/download/$tag"
  download_dir="$(mktemp -d)"
  cd "$download_dir"
  curl -fSL "$base/$package" -o "$package"
  curl -fsSL "$base/SHA256SUMS" -o SHA256SUMS
  awk -v file="$package" '$2 == file { print }' SHA256SUMS | sha256sum -c -
  sudo snap install --dangerous --classic "./$package"
  /snap/bin/astrona --version
)
```

The Snap bundles Kind, kubectl, ORAS, Git and CA certificates. Classic confinement lets lab scripts use your host files, kubeconfig and Docker/Podman sockets. Install and start a host container engine separately; optional VM tools also come from the host.

`--dangerous` is Snap's option for installing a local file without a Store assertion. Check the downloaded package against the release's `SHA256SUMS` first. This builds a local-installable Snap; publishing `snap install astrona --classic` requires registration of the Store name and approval for classic confinement. Locally installed Snaps do not update automatically. Installing a newer local file with the command above updates it. Remove with `sudo snap remove astrona`.

All three formats preserve `~/.astrona` on removal. Destroy running labs before uninstalling. CI builds packages for both architectures, installs and reinstalls them with real package managers, checks bundled tool discovery and upgrade protection, then verifies removal preserves user data.

## Quick install (macOS & Linux)

```sh
mkdir -p ~/.local/bin && \
OS=$(uname -s | tr '[:upper:]' '[:lower:]') && \
ARCH=$(uname -m) && \
[ "$ARCH" = "x86_64" ] && ARCH="amd64" || true && \
[ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ] && ARCH="arm64" || true && \
curl -L -o ~/.local/bin/astrona "https://github.com/astrona-io/astrona-cli/releases/latest/download/astrona-${OS}-${ARCH}" && \
chmod +x ~/.local/bin/astrona
```

Make sure `~/.local/bin` is on your shell's `PATH`.

## Manual install

1. Go to [GitHub Releases](https://github.com/astrona-io/astrona-cli/releases).
2. Download the binary for your platform: `astrona-darwin-arm64`, `astrona-darwin-amd64`, `astrona-linux-arm64`, or `astrona-linux-amd64`.
3. Make it executable and move it onto your `PATH`:

   ```sh
   chmod +x astrona-<os>-<arch>
   mv astrona-<os>-<arch> ~/.local/bin/astrona
   ```

## Verify

```sh
astrona check
```

This prints a ✓/⚠/✗ per item and exits non-zero only on a ✗. It checks three things:

**Dependencies.** A missing *required* tool is a ✗; optional ones (qemu toolchain, `git`) only warn, since they're only needed for specific runtimes or flags. Each missing tool says how to install it — with Homebrew installed that's the `brew install` command, and the end of the list gives one line for everything missing:

```text
Install what's required: brew install kind podman kubernetes-cli
Optional tools, all at once: brew install git qemu oras cdrtools
```

`astrona doctor` prints the same line for missing required tools.

**Container engine.** Whether Docker/Podman is actually running (a stopped `podman machine` or Docker daemon is the most common cause of confusing kind errors), and whether it has enough resources — for Docker Desktop and `podman machine` that's the VM's memory/CPUs, not your Mac's:

| Check | ✗ | ⚠ |
|---|---|---|
| Engine reachable (`docker info` / `podman info`) | not running | — |
| Memory | under 2 GiB | under 4 GiB |
| CPUs | — | 1 CPU |
| Rootless engine | on cgroup v1 (kind needs v2) | — |
| Linux only: `fs.inotify.max_user_watches` / `max_user_instances` | — | under 524288 / 512 (multi-node kind fails with "too many open files") |

Each ⚠/✗ prints the command that fixes it, e.g. `podman machine stop && podman machine set --memory 8192 --cpus 4 && podman machine start`.

**Your lab** — when a lab config is found (`-c`/`--git`, the lab picked with `astrona use`, else `./config.yaml`). No config in the current directory just means no lab section; a config that is there but can't be loaded (malformed YAML, a lab picked with `astrona use` that has moved) is a ✗ under `Lab`, with the error:

```sh
astrona check -c ./labs/my-lab
```

```text
Lab astro-my-lab:
  ✓  lab memory estimate                    ~6.9 GiB for this lab of 9.7 GiB
  ✓  host port 8080                         gateway http, free
  ✗  host port 18290                        port forward 'web', in use
        fix: find the user with `lsof -nP -iTCP:18290 -sTCP:LISTEN`, or change the port in the lab config
```

- **Memory estimate** — a rough figure from the lab's nodes and addons, plus astrona kind labs already running; ⚠ when it's over 75% of the engine's memory.
- **Host ports** — every `portForwards` host port and the gateway addon's ports must be free (skipped while the lab itself is running).
- The lab config is validated too (`runtime.kind`, `runtime.portForwards`).

### When something isn't working: `astrona doctor`

`astrona doctor` runs all of the above **and** looks at the running lab, then says what's wrong and how to fix it:

```text
Running lab astro-linked-labs-01:
  ✓  nodes                                  Ready (1/1)
  ✓  linked cluster idp                     Ready (1/1) · idp.astrona.internal
  ✗  port forward idp                       Stopped · http://127.0.0.1:30080
        fix: astrona port-forward start
  ⚠  pods in astro-linked-labs-01-idp       1 unhealthy: auth/broken: ErrImagePull (…)
        fix: astrona diagnose collects their logs and events

Error: 1 problem(s) found — the ✗ lines above say how to fix each
```

It checks this machine (tools, container engine, inotify), the lab (its config, whether this astrona may run it, memory and free ports when it isn't running) and the running lab (nodes, linked clusters, port forwards, unhealthy pods, exam clock). It only reads — nothing is started or changed — and exits non-zero on any ✗ — including a lab you named (argument, `-c`, `--file`, `--git`, `astrona use` — `astrona doctor .` counts) that isn't there or whose config doesn't parse; only when no lab is named and the current directory has none does it check the machine alone. `--bundle` also writes the full [diagnostics bundle](../guides/ci-integration.md#diagnostics-on-failure). Unhealthy pods are ⚠, not ✗: in a lab they may be the exercise.

## Local data and cache cleanup

### Automatic cache limits

Disposable caches have a default retention of **30 days** and a combined maximum
size of **50 GB**. Change them with:

```sh
astrona settings set cache-retention-days 30
astrona settings set cache-max-size-gb 50
astrona settings set cache-max-size-gb 0  # unlimited size; age limit still applies
```

Astrona checks the limits when operational commands finish, including commands
that fail after setup. It deletes expired entries first, then the oldest remaining
entries until their combined size fits the budget. Age uses filesystem modification
times; for a Git checkout, the newest entry within it determines its age. Reading
a cache file alone does not reset its age. Images and their metadata are removed
together. GB means 1,000,000,000 bytes, measured as logical file sizes.

The limits cover downloaded images/addons, cached Git checkouts, catalog metadata
and update-check metadata. They do not cover lab disks, student resources, results,
logs, bundles, installed tools, credentials, or Docker/Podman storage.

This is cleanup between commands, not a background service or a download quota.
Nothing expires while the CLI is not running. If any saved lab state exists,
including a stopped VM, **all cache eviction is deferred** to protect dependencies.
Cleanup also defers if another CLI command holds the maintenance lock. Usage can
therefore exceed the limits while downloads or labs need it. A notice on stderr
explains deferrals and reports removed entries without changing JSON stdout or
the original command's exit code. Help, completions, settings, data inspection and
internal background commands do not trigger automatic deletion.

Preview or explicitly apply just the configured policy:

```sh
astrona data clean --policy
astrona data clean --policy --yes
astrona data clean --policy --yes --dry-run  # preview only
```

Settings changes take effect on the next operational command or policy cleanup;
changing a setting does not itself delete anything. Policy cleanup needs valid
settings. Ordinary `data clean` and `data reset` remain usable for repair when
settings are malformed. `--policy` cannot be combined with `--temp-only`.

### Inspect and clean manually

Inspect local files without connecting to Astrona or starting a container engine:

```sh
astrona data
astrona data -o json
```

The report lists file counts and logical byte sizes under `~/.astrona` and the
Astrona Git cache (`~/Library/Caches/astrona/repos` on macOS, normally
`~/.cache/astrona/repos` on Linux). It includes saved results, logs, VM state and
installed CLI versions, but does not read their contents or follow symlinks.
Logical sizes can exceed actual disk use for sparse VM disks. Docker/Podman
storage and progress saved to your account are outside this report.

Preview cleanup before applying it:

```sh
astrona data clean
astrona data clean --yes
astrona data clean --temp-only
astrona data clean --temp-only --yes
```

Cleanup removes downloaded image/addon caches, cached Git checkouts, and the
catalog/update-check metadata. Those files will need to be downloaded again;
check that you no longer need them offline. `--temp-only` limits deletion to
partial image/addon downloads older than 24 hours inside Astrona's cache. It
does not scan the system temp directory or delete unrelated temporary files.

If any saved lab, VM, Kind, exam or port-forward state exists, caches are kept.
Stopped VMs can still depend on cached backing images. Use `astrona list` to
inspect your labs and `astrona destroy <lab>` to remove those you no longer need.
Do not delete state files to bypass this check. Symlinked cache locations are
rejected, and symlinks at the top of a cache are kept for manual inspection.

Ordinary commands hold a shared maintenance lock; confirmed cleanup or reset
requires an exclusive lock and fails immediately if another command holds it.
Older CLI releases do not take this lock. Finish those commands before applying
maintenance. The `.maintenance.lock` and `settings.json.lock` files stay in place;
their presence does not mean a process is running. Never delete them to unlock
the CLI.

Reset saved CLI preferences and the selected lab:

```sh
astrona data reset
astrona data reset --yes
```

Reset deletes only `settings.json`, `current.json`, `update-check.json`, and
`catalog.json`. It works even if a settings file contains invalid JSON. This
restores preference defaults and clears the `astrona use` selection; it does not
restart or destroy a lab. Credentials, trust approvals, custom catalog sources,
results, resources, bundles, logs and installed tools are preserved. Use
`astrona logout` separately if you want to sign out.

Both commands accept `-o json`. Without `--yes` they only preview; `--dry-run`
always prevents deletion, even when combined with `--yes`. The CLI cannot restore
deleted files. To remove old logs or unused CLI releases, use the existing commands:

```sh
astrona logs clean --older-than 168h --keep 10
astrona versions list
```

## Persistent CLI settings

`astrona settings` lists your preferences and shows whether each value is saved
or comes from the default. Preferences live in `~/.astrona/settings.json`,
separate from credentials, lab state, and remote-code approvals.

```sh
astrona settings
astrona settings set keep-context true
astrona settings set parallel 2
astrona settings set cache-retention-days 30
astrona settings set cache-max-size-gb 50
astrona settings get keep-context
astrona settings unset keep-context
astrona settings path
astrona settings -o json
```

| Setting | Default | Effect |
|---|---|---|
| `verbose` | `false` | Stream full command output instead of the compact step view |
| `keep-context` | `false` | Leave kubectl's current-context unchanged on commands with `--keep-context` |
| `parallel` | `1` | Start up to 1–5 linked clusters at once on commands with `--parallel` |
| `update-check` | `true` | Check for new CLI releases and show update notices |
| `cache-retention-days` | `30` | Maximum disposable-cache age; whole days from 1 to 36500 |
| `cache-max-size-gb` | `50` | Combined disposable-cache limit in decimal GB; `0` means unlimited size |

Explicit flags override saved values, including `--verbose=false`,
`--keep-context=false`, and `--parallel=1`. `--update-check=false` disables
update checks for one command; save it with
`astrona settings set update-check false` to keep that preference. The existing
`ASTRONA_NO_UPDATE_CHECK` environment variable, CI detection, and non-terminal
output still suppress update checks even when the setting is true.

Only saved overrides are written to the JSON file. `unset` removes one override
without changing the others; omitted keys use the built-in defaults. Listing
or reading settings does not create a file. The JSON output contains configured
values plus defaults, before flags or environment-based suppression are applied.

Writes are atomic, with file permissions `0600` and a `0700` directory when
created. A lock prevents simultaneous CLI edits from overwriting each other;
retry if another command is changing settings. Settings commands do not contact
the network, fetch a lab, or change your sign-in.

You can also edit the JSON file directly. Values must be JSON booleans or whole
numbers for `parallel` and the cache settings. `parallel` accepts 1 to 5;
`cache-max-size-gb` accepts 0 to 9223372036 without overflowing the byte count.
Unknown keys and invalid values are rejected
before a normal command runs. A damaged file is never silently overwritten:
use `astrona settings path` to find and repair it. Settings and lock files must
not be symlinks. Remote-code trust and automatic confirmation cannot be enabled
through settings.

Cache limits are local preferences, not lab flags, and are not forwarded during
version handover. Older CLI releases that validate settings may reject these new
keys; remove the overrides or upgrade that binary before using it.

## Shell completion

Installed with Homebrew? Completion for bash, zsh and fish is already set up — skip the steps below (Homebrew's shell setup must be loaded, see `brew info astrona`).

`astrona completion <shell>` prints a completion script for bash, zsh, fish or PowerShell. Besides commands and flags, it completes **lab names** from what's actually on your machine — `astrona destroy <TAB>`, `stop`, `start`, `shell`, `kubeconfig`, `diagnose`, `ssh`, `port-forward list|stop` — filtered to what makes sense (`start` offers stopped labs, `ssh` qemu VMs), each with its runtime and status. Type part of the name without `astro-` and the short form is completed.

It also completes:

- `--cluster <TAB>` (`shell`, `kubeconfig`, `net`, `reset`) — the lab's [linked clusters](../guides/linked-labs.md)
- `astrona versions install <TAB>` — published releases you don't have yet; `versions remove <TAB>` — the installed ones
- a lab directory or config file wherever a command takes one (`astrona use ./<TAB>`, `astrona run ./<TAB>`)

=== "zsh"

    ```sh
    astrona completion zsh > "${fpath[1]}/_astrona"   # then start a new shell
    ```

=== "bash"

    ```sh
    astrona completion bash > ~/.local/share/bash-completion/completions/astrona
    ```

=== "fish"

    ```sh
    astrona completion fish > ~/.config/fish/completions/astrona.fish
    ```

## Staying up to date

```sh
astrona upgrade
```

`astrona upgrade` checks GitHub for the latest release, downloads the binary for your OS/architecture, verifies it against the SHA-256 digest GitHub records for it (and its [build provenance](../guides/astrona-versions.md#build-provenance) when the GitHub CLI is installed), and atomically replaces the currently running executable — only when the release is newer than yours. On a [Homebrew](#homebrew-macos-linux) install it runs `brew upgrade astrona` instead (`brew reinstall astrona` with `--force`), so Homebrew keeps track of the install.

On DEB, RPM and Snap installs, `astrona upgrade` prints the package installation command and exits without modifying package-owned files, including with `--force`. Download and install the newer package using the instructions above.

astrona tells you when a newer release exists — `[INFO] astrona v0.2.3 is available (you have v0.2.2) — astrona upgrade · what's new: <release notes>` — at most **once a day**. It asks GitHub at most once a day too (cached in `~/.astrona/update-check.json`), so commands don't wait on the network. It never checks in CI (`CI`/`GITHUB_ACTIONS`/… set) or when stderr isn't a terminal; `ASTRONA_NO_UPDATE_CHECK=1` turns it off everywhere.

Older releases can be installed next to the latest — see [Astrona Versions](../guides/astrona-versions.md).

## Next

Continue to the [Quickstart](quickstart.md) to run your first lab.
