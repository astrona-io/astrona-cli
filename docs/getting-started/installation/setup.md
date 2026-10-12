# Set up and verify Astrona

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

After [installing Astrona](../installation.md), one command gets the rest of the machine ready:

```sh
astrona setup             # shows each step and asks before doing it
astrona setup --dry-run   # only show the plan
astrona setup --yes --accept-terms  # explicitly approve steps and CLI terms
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

It checks this machine (tools, container engine, inotify), the lab (its config, whether this astrona may run it, memory and free ports when it isn't running) and the running lab (nodes, linked clusters, port forwards, unhealthy pods, exam clock). It only reads — nothing is started or changed — and exits non-zero on any ✗ — including a lab you named (argument, `-c`, `--file`, `--git`, `astrona use` — `astrona doctor .` counts) that isn't there or whose config doesn't parse; only when no lab is named and the current directory has none does it check the machine alone. `--bundle` also writes the full [diagnostics bundle](../../guides/ci-integration.md#diagnostics-on-failure). Unhealthy pods are ⚠, not ✗: in a lab they may be the exercise.

