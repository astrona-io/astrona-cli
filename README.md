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

| Command | What it does |
|---|---|
| `astrona run` | Create the lab environment (kind cluster or qemu VM) and run bootstrap (init scripts + manifests). |
| `astrona destroy` | Run teardown scripts, then tear down the lab environment (unless `keepCluster: true`). |
| `astrona submit` | Submit the current lab state to the Proctor for grading and report its PASS/FAIL verdict. Exit code reflects the result — this is the grading gate, not a self-check. This is the student-facing flow: take a lab, submit for grading. Accepts `--junit-xml=<path>`. |
| `astrona test` | Full CI pipeline in one shot: bootstrap → testing (apply reference solution) → submit to the Proctor → teardown. Always cleans up, even on failure. A lab author wires this into their own CI to prove the lab's reference solution actually passes the Proctor's own checks before publishing — not part of the student flow. Accepts `--junit-xml=<path>`. Runs against a `test-`-prefixed cluster name so it never collides with a lab you already have up via `astrona run`. |
| `astrona check` | Check that astrona's dependencies are installed: `kind`, `docker`/`podman`, and `kubectl` are required; the qemu toolchain and `git` are optional (only needed for `runtime.type: qemu` or `--git`) and only warn if missing. Non-zero exit only when a required dependency is missing. |
| `astrona upgrade` | Check for the latest release on GitHub, download the compiled binary for the active OS and architecture, and atomically replace the current running executable. |

## Lab config

```yaml
metadata:
  name: "k8s-basics-01"
  docs:
    prerequisites: "docs/prerequisites.md"    # knowledge/tooling needed before attempting
    examQuestion: "docs/exam-question.md"     # formal, self-contained task statement
    caseStudy: "docs/case-study.md"           # softer, hint-driven version of the same task
    guide: "docs/step-by-step-guide.md"       # full walkthrough with the answer

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

You can download and install pre-compiled binaries of `astrona-cli` directly from GitHub Releases.

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
