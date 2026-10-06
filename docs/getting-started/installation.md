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

**Dependencies.** A missing *required* tool is a ✗; optional ones (qemu toolchain, `git`) only warn, since they're only needed for specific runtimes or flags.

**Container engine.** Whether Docker/Podman is actually running (a stopped `podman machine` or Docker daemon is the most common cause of confusing kind errors), and whether it has enough resources — for Docker Desktop and `podman machine` that's the VM's memory/CPUs, not your Mac's:

| Check | ✗ | ⚠ |
|---|---|---|
| Engine reachable (`docker info` / `podman info`) | not running | — |
| Memory | under 2 GiB | under 4 GiB |
| CPUs | — | 1 CPU |
| Rootless engine | on cgroup v1 (kind needs v2) | — |
| Linux only: `fs.inotify.max_user_watches` / `max_user_instances` | — | under 524288 / 512 (multi-node kind fails with "too many open files") |

Each ⚠/✗ prints the command that fixes it, e.g. `podman machine stop && podman machine set --memory 8192 --cpus 4 && podman machine start`.

**Your lab** — when a lab config is found (`-c`, default `./config.yaml`):

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

## Shell completion

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

Checks GitHub for the latest release, downloads the binary for your OS/architecture, verifies it against the SHA-256 digest GitHub records for it, and atomically replaces the currently running executable — only when the release is newer than yours.

astrona tells you when a newer release exists — `[INFO] astrona v0.2.3 is available (you have v0.2.2) — astrona upgrade · what's new: <release notes>` — at most **once a day**. It asks GitHub at most once a day too (cached in `~/.astrona/update-check.json`), so commands don't wait on the network. It never checks in CI (`CI`/`GITHUB_ACTIONS`/… set) or when stderr isn't a terminal; `ASTRONA_NO_UPDATE_CHECK=1` turns it off everywhere.

Older releases can be installed next to the latest — see [Astrona Versions](../guides/astrona-versions.md).

## Next

Continue to the [Quickstart](quickstart.md) to run your first lab.
