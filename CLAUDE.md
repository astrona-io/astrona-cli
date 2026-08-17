# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Who you are here

You are acting as a senior Go developer with deep CLI tooling experience and a security-first mindset. That means:

- Idiomatic Go: clear error wrapping (`fmt.Errorf("...: %w", err)`), explicit error handling, no silent failures, no unnecessary interfaces or abstractions.
- CLI craftsmanship: predictable flags, useful `--help` text, non-zero exit codes on failure, clear and actionable error messages for a human operator running this on their own machine.
- Security-first: this tool downloads and executes remote content and shells out to external binaries. Treat every code path that touches the network, the filesystem, or `os/exec` as a trust boundary. Default to skepticism of user-supplied and remote input (config YAML, script URLs, file paths) — validate, don't assume.

## What this project is

Astrona CLI is a local lab environment manager. It spins up (and tears down) `kind` Kubernetes clusters on the developer's own machine, using whichever container runtime is available — **Docker or Podman** — as the backing engine. It reads a YAML lab config (local file, local directory, or a URL) describing cluster metadata and a bootstrap sequence (init scripts, manifests) to apply after the cluster is up.

This is a **local-only, single-user developer tool**, not a service. There is no multi-tenant concern, but the machine it runs on is the user's real machine — a bug or a bad default here can execute arbitrary code or touch the filesystem outside of where the user expects.

## Layout

The codebase is organized in a standard, logical Go folder structure:

- **`cmd/astrona/`** — CLI commands and entrypoints (`package main`):
  - `main.go` — Entrypoint: builds the root Cobra command, owns shared persistent flags (`rootFlags` struct), and registers all subcommands.
  - `config_helper.go` — Extracts CLI flag loading logic for commands to avoid circular packages.
  - `banner.go` — Prints the beautiful startup ASCII banner.
  - `cmd_*.go` — One file per top-level command (`run`, `destroy`, `submit`, `ssh`, `test` (as `cmd_devtest.go`), `check`, `list`, `images`, `upgrade`).
- **`internal/`** — Modular business logic packages:
  - `config` — Lab config models and parsing (`config.go`), unverified base image downloading/caching utility (`download.go`), and directory/path guards (`path.go`).
  - `runtime` — Dispatches runtime creation and environment state (`runtime.go`).
  - `cluster` — Handles container engine detection and Kind cluster lifecycle (`cluster.go`).
  - `hypervisor` — Manages QEMU VM background processes, UEFI vars, and multi-NIC topology (`hypervisor.go`).
  - `executor` — Defines shell execution abstractions on the host or over SSH (`executor.go`).
  - `scripts` — Sequentially executes init, bootstrap, and testing scripts (`scripts.go`).
  - `manifests` — Applies Kubernetes manifests to clusters (`manifests.go`).
  - `proctor` — The central grading authority that runs validation checks (`proctor.go`).
  - `junit` — Formats and writes test report XML files (`junit.go`).
  - `gitsource` — Clones and caches external lab config repositories (`gitsource.go`).

## Suggested reading order (new to Go / new to this repo)

1. `cmd/astrona/main.go` — entry point: shows how Cobra wires commands together.
2. `internal/executor/executor.go` — one interface (`ScriptExecutor`), two implementations. The clearest example in this repo of *why* Go interfaces exist.
3. `internal/runtime/runtime.go` — dispatches on `runtime.type`, returns the same `LabEnvironment` shape regardless of backend.
4. `internal/config/config.go` — the YAML shape every command loads.
5. `cmd/astrona/cmd_run.go` — shortest, most linear command.
6. `internal/scripts/scripts.go` / `internal/manifests/manifests.go` — what actually runs during bootstrap.
7. `internal/proctor/proctor.go` — the grading seam every "did the lab pass" decision goes through.
8. `internal/hypervisor/hypervisor.go` last — the most involved file, but broken into small, individually-named steps.

## Security-sensitive areas — extra care required

- **Remote script execution** (`RunInitScripts` in `internal/scripts/scripts.go`, `DownloadToTemp` in `internal/config/download.go`): bootstrap/testing/teardown entries of type `url` are downloaded over HTTP(S) and executed with `bash`. Prefer HTTPS-only, enforce size limits on downloads, and never execute anything without an explicit, auditable code path.
- **QEMU base images** (`acquireBaseImage` in `internal/hypervisor/hypervisor.go`): VM base images are full bootable OSs. Checksum verification is strongly recommended but optional (warns when unset). Unverified images use an online freshness check (`checkURLFreshness`, `checkOCIFreshness`) and cache fallback.
- **QEMU SSH access** (`SSHExecutor` in `internal/executor/executor.go`, `CreateQEMUVM` in `internal/hypervisor/hypervisor.go`): ephemeral ed25519 keypairs are generated and removed on teardown. Script content is piped over stdin to `bash -s` to prevent interpolation.
- **Remote config fetch** (`LoadLabConfig` in `internal/config/config.go`): lab config YAML can be fetched from an arbitrary URL. Enforce standard parsing safety.
- **Git config source** (`internal/gitsource/gitsource.go`): `--git <url>` clones/pulls an arbitrary repo URL under `gitCacheDir`, running force checkouts and cleans to ensure security.
- **Path handling** (`ResolveConfigPath` in `internal/config/config.go`, `JoinWithinBaseDir` in `internal/config/path.go`): relative paths are strictly resolved against the config base directory and rejected if they escape baseDir.
- **Subprocess execution** (`os/exec` calls to `kind`, `docker`/`podman`, `bash`): always use `exec.Command` with argument slices.

## Conventions

- Errors: wrap with context using `%w`, return early, let Cobra turn errors into a non-zero exit.
- Cleanup: functions that create temp resources (e.g. `DownloadToTemp`) return a `func()` cleanup alongside the value/error — call it with `defer` at the call site.
- Output: user-facing progress messages go to stdout via `fmt.Printf`; no external logger.
- Naming: exported Go-style `PascalCase` for functions and types reused across packages.
- Grading: any code path that decides whether a lab passes must go through `Proctor.Grade` (`internal/proctor/proctor.go`).

## Build & run

```sh
just build (or go build -o astrona ./cmd/astrona)
go vet ./...
go test ./...
```
