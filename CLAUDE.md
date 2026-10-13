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
  - `cmd_workspace.go`, `workspace_model.go` — Opt-in Kind terminal workspace (`run --ui`, `ui [lab-name]`). Bubble Tea owns terminal input/rendering; restore the terminal before calling the shared shell, submit or destroy flows. Polling only reads isolated kubeconfigs and saved state, with bounded concurrency, output and deadlines. It must never grade, submit, renew sessions or destroy a lab. Require explicit destruction confirmation and reject actions after the saved run changes. Opening the UI is not a saved preference. `StartedAt` in lab state is local readiness time; a session's `ExpiresAt` is not a lab deadline. QEMU is deliberately unsupported in this first version.
  - `workspace_settings.go` — The workspace's `p` editor uses `settingsPath`, existing definitions and validation. File reads/writes run outside Bubble Tea. Saves require confirmation, share the settings lock, preserve unrelated edits and reject stale values for the selected key. Never approve new settings definitions or usage terms through this editor. Saved preferences do not reload the current command's options; cache maintenance runs only after the workspace exits. Settings remain accessible when lab actions are blocked.
  - `cmd_data.go`, `data_lock.go` — Local file usage and preview-first cleanup/reset. The entrypoint holds a shared maintenance lock during command setup and execution; confirmed maintenance takes an exclusive lock. Keep the `data` command independent of settings, lab selection and network hooks so it can repair broken local preferences.
  - `cache_policy.go` — After operational commands return, release the shared lock and try exclusive policy cleanup. Never clean during setup, a running command, or panic unwinding. Preserve the command's exit code and JSON stdout; report removed caches and deferrals on stderr. Commands exempt from settings review, including stop/destroy and logout, must not auto-clean or check releases using unreviewed preferences. Only `data clean --policy` reads preferences within the data subtree.
  - `cmd_settings.go`, `settings_store.go` — Validated user preferences in `~/.astrona/settings.json`; explicit flags override saved values. Writes use a file lock and atomic replacement. Credentials and trust remain separate.
  - `settings_review.go`, `cmd_settings_review.go` — Read-only `settings view` and content-based upgrade review in `settings-review.json`. The entrypoint wraps every command hook: terms first, settings review next, then operational setup. Keep help, local maintenance, background helpers, stop/destroy and logout available without review. New/default/description/revision changes require acknowledgement; bump a setting's review revision when its behavior changes without a text change. Network/privacy settings must require an explicit choice; future optional tracking must default off and needs its own documented controls. Settings review is not tracking consent. CI approval binds to definitions and saved values; never add a blanket `--yes` bypass. Update the pinned test review ID only after reviewing the changed definitions and choices. Review and settings edits share `settings.json.lock` with data reset, which removes both preferences and the review receipt.
  - `cmd_terms.go` — Entrypoint terms gate, wrapped around every command's persistent setup hook. Acceptance is separate from remote-code trust and `--yes`; never add a saved auto-accept setting. Keep the explicit cleanup/help exemptions narrow.
  - `banner.go` — Prints the beautiful startup ASCII banner.
  - `cmd_*.go` — One file per top-level command (`run`, `destroy`, `submit`, `ssh`, `test` (as `cmd_devtest.go`), `check`, `list`, `images`, `upgrade`).
- **`internal/`** — Modular business logic packages:
  - `apphome` — Build profile selected once at entrypoint startup from `main.Version`, before any state access or background work. Unversioned builds use `~/.astrona-dev`, Git caches beneath `cache/repos`, and `astrodev-` / `astrodev-test-` cluster names. Releases retain their original paths and prefixes. All managed paths and discovery must use this package; never hardcode a release path in operational code. Do not migrate credentials, settings or consent between profiles. Developer binaries must not self-upgrade or hand commands to release binaries. This is state isolation, not a host or server sandbox.
  - `terms` — Bundled CLI usage notice and a private, atomically replaced `~/.astrona/terms.json` receipt. Match both revision and content digest. CI requires `--accept-terms` on every operational invocation, even with a receipt; missing noninteractive consent must fail without reading stdin. Notice updates require a CLI release. Update `notice.txt` and `Revision` together after legal review; this acknowledgement does not change Apache-2.0 or provide a server audit. Native Windows setup and WSL use separate receipts.
  - `localdata` — Read-only file inventory and allowlisted deletion plans. Cache cleanup is blocked by saved lab state, including stopped VMs. Never treat VM disks, results, credentials, resources or bundles as disposable cache. Use bounded `os.Root` operations and preserve maintenance/settings lock inodes.
    `PlanPolicy` uses the same safety boundary: default 30 days and 50 decimal GB, configured by `cache-retention-days` and `cache-max-size-gb` (0 disables only the size cap). Evict by newest filesystem modification time per entry, expiry first and oldest first for size; group image data and sidecars. These are between-command limits, not hard disk quotas. Do not relax saved-state protection just to meet a limit.
  - `config` — Lab config models and parsing (`config.go`), unverified base image downloading/caching utility (`download.go`), and directory/path guards (`path.go`).
  - `runtime` — Dispatches runtime creation and environment state (`runtime.go`).
  - `lifecycle` — The lab pipeline every command uses: `Up` (create → links → preload → addons → lab CA → DNS → bootstrap → readiness → port forwards), linked clusters (`StartLinkedClusters`, dependency order, `--parallel`), stable names and WAN, shared CA, linked-cluster teardown, `SoftReset`. Printing, prompts and flags stay in `cmd/astrona`.
  - `version` — Release versions and `astronaVersion` constraints.
  - `labshell` — Student Kind terminals run in a tools container. Only the per-run `runtime/<lab>/work` directory is writable from the terminal; never mount host homes, engine sockets or SSH agents. Keep failure closed: no host shell fallback. UI image preparation must be cancellable without holding the rendering lock. QEMU terminals remain inside their VM with SSH local escapes disabled.
  - `cluster` — Handles container engine detection and Kind cluster lifecycle (`cluster.go`).
  - `hypervisor` — Manages QEMU VM background processes, UEFI vars, and multi-NIC topology (`hypervisor.go`).
  - `executor` — Defines shell execution abstractions on the host or over SSH (`executor.go`).
  - `scripts` — Sequentially executes init, bootstrap, and testing scripts (`scripts.go`).
  - `manifests` — Applies Kubernetes manifests to clusters (`manifests.go`).
  - `proctor` — The central grading authority that runs validation checks (`proctor.go`).
  - `junit` — Formats and writes test report XML files (`junit.go`).
  - `gitsource` — Clones and caches external lab config repositories (`gitsource.go`).
  - `labstate` — What astrona remembers per running lab in `~/.astrona/labs/<lab>.json` (0600): its config source (so `submit` can find the only running lab), the kubectl context `run` switched away from (restored by `destroy`), and its Astrona lab session (never a token).

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
- **QEMU SSH access** (`SSHExecutor` in `internal/executor/executor.go`, `CreateQEMUVM`/`buildCloudInitSeed` in `internal/hypervisor/hypervisor.go`): each VM gets two accounts, each with its own ephemeral ed25519 keypair generated and removed on teardown. `student` (`qemuSSHUser`) is the human-facing account `astrona ssh` connects as. `astrona` (`adminSSHUser`) is a dedicated superuser the CLI itself uses to run init/bootstrap/testing/teardown scripts (`sshExecutorFor` in `internal/runtime/runtime.go` always builds the `SSHExecutor` from the admin identity, never the student one) — kept independent so a lab can lock down `student` later (drop its sudo, disable its password auth) without breaking the CLI's own automation. Root SSH login stays disabled (`disable_root: true`). Script content is piped over stdin to `bash -s` to prevent interpolation.
- **Remote config fetch** (`LoadLabConfig` in `internal/config/config.go`): lab config YAML can be fetched from an arbitrary URL. Enforce standard parsing safety.
- **Git config source** (`internal/gitsource/gitsource.go`): `--git <url>` clones/pulls an arbitrary repo URL under `gitCacheDir`, running force checkouts and cleans to ensure security.
- **Path handling** (`ResolveConfigPath` in `internal/config/config.go`, `JoinWithinBaseDir` in `internal/config/path.go`): relative paths are strictly resolved against the config base directory and rejected if they escape baseDir.
- **Subprocess execution** (`os/exec` calls to `kind`, `docker`/`podman`, `bash`): always use `exec.Command` with argument slices.

## Conventions

- Errors: wrap with context using `%w`, return early, let Cobra turn errors into a non-zero exit.
- Cleanup: functions that create temp resources (e.g. `DownloadToTemp`) return a `func()` cleanup alongside the value/error — call it with `defer` at the call site.
- Output: user-facing progress messages go to stdout via `fmt.Printf`; no external logger. Warnings/notices via `ui.Warnf`/`ui.Infof` (stderr, colored only on a terminal).
- Prompts: a confirmation takes `--yes`/`-y`; approving someone else's code (remote lab, another astrona binary) takes its own explicit flag (`--trust`, `--install-version`), never `-y`. Without a terminal, never block — fail and name the flag.
- Exit codes: `notPassed(...)` (exit 2) for "graded, didn't pass"; any other error exits 1.
- Naming: exported Go-style `PascalCase` for functions and types reused across packages.
- Grading: any code path that decides whether a lab passes must go through `Proctor.Grade` (`internal/proctor/proctor.go`).
- Docs: any change that adds/renames a flag, command, config field, or alters user-facing behavior must update `docs/` (mkdocs site — `docs/reference/cli/` for commands, `docs/reference/lab-config.md` for config fields, `docs/concepts/` for behavior/architecture changes, `docs/guides/` for workflow changes) in the same change, not as a follow-up.

## Build & run

The docs theme follows the public frontend's Mission Control tokens. Styling is
in `docs/stylesheets/extra.css`, templates in `docs/overrides/`, and optional
motion controls in `docs/javascripts/motion.js`. Keep Material's built-in search,
navigation, code copying and mike version selection. Theme changes require a
strict docs build and desktop/mobile browser checks; see `docs/contributing.md`.
Never make documentation depend on animation or JavaScript to be readable.

```sh
just build # creates ./astrona only; development data goes to ~/.astrona-dev
./astrona --help
go vet ./...
go test ./...
```

Plain `go build -o astrona ./cmd/astrona` also produces a development build.
Release packaging sets `-X main.Version=vX.Y.Z` and uses release state. There is
no Justfile install recipe; never move local test binaries onto the user's PATH.
