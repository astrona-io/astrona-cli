# Architecture reference

## Layout

The codebase is organized in a standard, logical Go folder structure:

- **`cmd/astrona/`** — CLI commands and entrypoints (`package main`):
  - `main.go` — Entrypoint: builds the root Cobra command, owns shared persistent flags (`rootFlags` struct), and registers all subcommands.
  - `config_helper.go` — Extracts CLI flag loading logic for commands to avoid circular packages.
  - `cmd_workspace.go`, `workspace_model.go` — Opt-in Kind and QEMU terminal workspace (`run --ui`, `ui [lab-name]`). Bubble Tea owns terminal input/rendering; restore the terminal before calling the shared shell, submit or destroy flows. Polling only reads isolated kubeconfigs and saved state, with bounded concurrency, output and deadlines. It must never grade, submit, renew sessions or destroy a lab. Require explicit destruction confirmation and reject actions after the saved run changes. Opening the UI is not a saved preference. `StartedAt` in lab state is local readiness time; a session's `ExpiresAt` is not a lab deadline. QEMU terminals use the student SSH identity. Resource workspaces share editable files across terminal tabs, with no host execution bridge.
  - `workspace_settings.go` — The workspace's `p` editor uses `settingsPath`, existing definitions and validation. File reads/writes run outside Bubble Tea. Saves require confirmation, share the settings lock, preserve unrelated edits and reject stale values for the selected key. Never approve new settings definitions or usage terms through this editor. Saved preferences do not reload the current command's options; cache maintenance runs only after the workspace exits. Settings remain accessible when lab actions are blocked.
  - `cmd_data.go`, `data_lock.go` — Local file usage and preview-first cleanup/reset. The entrypoint holds a shared maintenance lock during command setup and execution; confirmed maintenance takes an exclusive lock. Keep the `data` command independent of settings, lab selection and network hooks so it can repair broken local preferences.
  - `cache_policy.go` — After operational commands return, release the shared lock and try exclusive policy cleanup. Never clean during setup, a running command, or panic unwinding. Preserve the command's exit code and JSON stdout; report removed caches and deferrals on stderr. Commands exempt from settings review, including stop/destroy and logout, must not auto-clean or check releases using unreviewed preferences. Only `data clean --policy` reads preferences within the data subtree.
  - `cmd_settings.go`, `settings_store.go` — Validated user preferences in `~/.astrona/settings.json`; explicit flags override saved values. Writes use a file lock and atomic replacement. Credentials and trust remain separate.
  - `settings_review.go`, `cmd_settings_review.go` — Read-only `settings view` and content-based upgrade review in `settings-review.json`. The entrypoint wraps command hooks for terms and startup approval, then operational setup. Settings review is explicit and optional; never block ordinary commands on a missing or outdated review receipt. Keep help, local maintenance, background helpers, stop/destroy and logout available without review. During explicit review, new/default/description/revision changes require acknowledgement; bump a setting's review revision when its behavior changes without a text change. Network/privacy settings must require an explicit choice; future optional tracking must default off and needs its own documented controls. Settings review is not tracking consent. Explicit CI review approval binds to definitions and saved values; normal CI runs need no settings approval or pinned review ID. Review and settings edits share `settings.json.lock` with data reset, which removes both preferences and the review receipt.
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

