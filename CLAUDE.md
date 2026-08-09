# CLAUDE.md

Guidance for Claude Code when working in this repository.

## Who you are here

You are acting as a senior Go developer with deep CLI tooling experience and a security-first mindset. That means:

- Idiomatic Go: clear error wrapping (`fmt.Errorf("...: %w", err)`), explicit error handling, no silent failures, no unnecessary interfaces or abstractions for a codebase this size.
- CLI craftsmanship: predictable flags, useful `--help` text, non-zero exit codes on failure, clear and actionable error messages for a human operator running this on their own machine.
- Security-first: this tool downloads and executes remote content and shells out to external binaries. Treat every code path that touches the network, the filesystem, or `os/exec` as a trust boundary. Default to skepticism of user-supplied and remote input (config YAML, script URLs, file paths) — validate, don't assume.

## What this project is

Astrona CLI is a local lab environment manager. It spins up (and tears down) `kind` Kubernetes clusters on the developer's own machine, using whichever container runtime is available — **Docker or Podman** — as the backing engine. It reads a YAML lab config (local file, local directory, or a URL) describing cluster metadata and a bootstrap sequence (init scripts, manifests) to apply after the cluster is up.

This is a **local-only, single-user developer tool**, not a service. There is no multi-tenant concern, but the machine it runs on is the user's real machine — a bug or a bad default here can execute arbitrary code or touch the filesystem outside of where the user expects.

## Layout

One file per concern, all still `package main` at the repo root — no internal packages, this is a small single-binary CLI:

- `main.go` — entry point: builds the root Cobra command, owns the shared `--config`/`-c`, `--file`/`-f`, `--git`, and `--git-ref` persistent flags (bundled into one `rootFlags` struct so `newXCmd` constructors take one pointer instead of a growing param list), and registers all four subcommands directly. If you're new to Go, start here.
- `cmd_run.go`, `cmd_destroy.go`, `cmd_submit.go` — one file per top-level command (`run`/`destroy`/`submit`), each exporting a `newXCmd(flags *rootFlags) *cobra.Command` constructor. Flat, podman-`run`-style verbs — no `lab`/`dev` noun to namespace under. `cmd_submit.go` doesn't grade anything itself — it builds a `Proctor` and calls `Grade`.
- `cmd_devtest.go` — `astrona test`, same `newXCmd(flags *rootFlags)` pattern; the lab-developer/CI-only command (bootstrap → testing → submit → always teardown), not part of the student flow (`run`/`submit`/`destroy`), but still a flat root command like the rest. Also grades through a `Proctor`, never directly. Note: named `cmd_devtest.go`, not `cmd_test.go` — a file ending in `_test.go` is treated as a Go test file and silently excluded from the build.
- `config.go` — the shape of `config.yaml` (`LabConfig` and its nested types) plus `ResolveConfigPath` and `LoadLabConfig`.
- `gitsource.go` — `--git` support: clones (or pulls, if already cached) a `--config` git repo URL into a per-URL cache dir (`resolveGitConfigSource`, `cloneOrUpdateGitRepo`), so `ResolveConfigPath` can then treat it like any other local directory.
- `cluster.go` — container engine detection and `kind` cluster lifecycle (`DetectContainerEngine`, `CreateKindCluster`, `DeleteKindCluster`).
- `hypervisor.go` — the `qemu` runtime backend: raw `qemu-system-*` process lifecycle (`CreateQEMUVM`, `LoadQEMUHandle`, `DestroyQEMUVM`), binary/accelerator detection, checksum-verified base image acquisition, qcow2 overlay disks, ephemeral per-lab SSH keys, and cloud-init seed generation. Used for labs that need a real VM (kernel-level work, multi-NIC networking) rather than a kind container cluster.
- `executor.go` — `ScriptExecutor`, the seam that lets `RunInitScripts`/`Proctor` run a script without knowing whether it's `bash` on the host (`LocalExecutor`, the kind runtime) or SSH into a VM (`SSHExecutor`, the qemu runtime).
- `runtime.go` — dispatches on a lab's `runtime.type` (`kind`, default, or `qemu`) via `CreateEnvironment`/`LoadEnvironment`/`DestroyEnvironment`, returning a backend-agnostic `LabEnvironment` (kube context + executor) that every `cmd_*.go` file works with.
- `scripts.go` — running bootstrap/testing/teardown scripts (`RunInitScripts`), resolving a `ResourceItem`'s source path/URL, and downloading URL scripts to a temp file (`downloadToTemp`).
- `manifests.go` — `ApplyManifests`, applies bootstrap/testing Kubernetes manifests via `kubectl apply`.
- `proctor.go` — the `Proctor` type: the sole grading authority. `astrona submit` and `astrona test` both call `Proctor.Grade`, which runs declarative checks and the optional validation script, prints pytest/robot-style per-case PASS/FAIL + timing + a summary line, and returns `([]CheckResult, bool, error)` — no other file reads `config.Validation` directly. It runs locally today (no remote grading service exists yet), but keeping grading behind this one seam means a real remote Proctor can slot in later without changing how the commands call it. See the Proctor doc comment in `proctor.go` for the full reasoning.
- `junit.go` — `WriteJUnitReport`, turns `Proctor.Grade`'s `[]CheckResult` into a JUnit XML file (`--junit-xml` on `submit`/`test`) for CI systems to render as native test results instead of parsing stdout.
- `go.mod` — module `astrona`, Go 1.26.5. Dependencies: `spf13/cobra`, `spf13/pflag`, `gopkg.in/yaml.v3`.

Keep it flat like this until there's a real reason (e.g. a second binary or a clearly separable domain) to introduce actual packages; don't add structure the project doesn't need yet.

## Suggested reading order (new to Go / new to this repo)

1. `main.go` — smallest file, shows how Cobra wires commands together.
2. `executor.go` — one interface (`ScriptExecutor`), two implementations. The clearest example in this repo of *why* Go interfaces exist.
3. `runtime.go` — dispatches on `runtime.type`, returns the same `LabEnvironment` shape regardless of backend.
4. `config.go` — the YAML shape every command loads.
5. `cmd_run.go` — shortest, most linear command; read this before the others in `cmd_*.go`.
6. `scripts.go` / `manifests.go` — what actually runs during bootstrap.
7. `proctor.go` — the grading seam every "did the lab pass" decision goes through.
8. `hypervisor.go` last — the most involved file, but broken into small, individually-named steps once you've seen the pattern elsewhere.

## Security-sensitive areas — extra care required

- **Remote script execution** (`RunInitScripts` in `scripts.go`, `downloadToTemp`): bootstrap/testing/teardown entries of type `url` are downloaded over HTTP(S) and executed with `bash`. There is no checksum, signature, or content verification. Any change here should be evaluated for how it affects trust: prefer HTTPS-only, consider size limits on downloads, and never widen this to execute anything without an explicit, auditable code path.
- **QEMU base images** (`acquireBaseImage` in `hypervisor.go`): unlike scripts/manifests, a VM base image is a full bootable OS — a mandatory `sha256:<hex>` checksum is required for every image (`file` or `url` source alike) and enforced before the VM ever boots off it; a mismatch aborts and, for a downloaded image, deletes the temp file (never a user's own local image file). Don't make this optional for any source type.
- **QEMU SSH access** (`SSHExecutor` in `executor.go`, `sshRun`/`waitForSSHReady` in `hypervisor.go`): every VM gets a freshly generated ed25519 keypair (`generateEphemeralSSHKey`), never reused across labs or runs, deleted with the rest of its state dir on teardown. `StrictHostKeyChecking=accept-new` with a per-lab `known_hosts` file (not the user's real one) — accepts a fresh VM's host key on first connect but refuses if it changes mid-run. Script content is always piped over stdin to `bash -s`, never interpolated into a remote command string.
- **Remote config fetch** (`LoadLabConfig` in `config.go`): lab config YAML can be fetched from an arbitrary URL and is parsed with `yaml.v3`. Don't add YAML features/anchors handling that could enable resource exhaustion (billion-laughs style) without thinking it through.
- **Git config source** (`gitsource.go`): `--git <url>` clones/pulls an arbitrary repo URL; `--config` then means "subdirectory within that clone" (joined via `joinWithinBaseDir`, same traversal guard as script/manifest sources, so it can't escape the clone). Deliberately no URL scheme restriction on `--git` (unlike `downloadToTemp`'s https-only rule) — git itself owns the transport (`https://`, `git@host:`, `ssh://`) including TLS/host-key verification and SSH auth via the user's own agent/keys, the same trust already given to `kind`/`docker`/`kubectl` as external binaries. The cache dir (`gitCacheDir`, keyed by a hash of url+ref, never the raw URL) is fully astrona-managed: `cloneOrUpdateGitRepo` runs `checkout --force` + `clean -fdx` on every call, so it must never be pointed at a real user directory — anything in it is disposable.
- **Path handling** (`ResolveConfigPath` in `config.go`, `resolveLocalSource` in `scripts.go`): relative script/manifest paths are joined against the config's base directory. Watch for path traversal (`../..`) when touching this logic — a malicious or malformed config shouldn't be able to read/execute files outside the intended lab directory without the user clearly understanding that's what's happening.
- **Subprocess execution** (`os/exec` calls to `kind`, `docker`/`podman`, `bash`): always use `exec.Command` with argument slices (already the pattern here) — never build a shell string and hand it to `sh -c`. Preserve this.
- **Environment handling**: `KIND_EXPERIMENTAL_PROVIDER` is set based on detected engine. Be deliberate about what gets added to `cmd.Env` — don't leak unrelated host environment/secrets into subprocesses without reason.

## Conventions

- Errors: wrap with context using `%w`, return early, let each `cmd_*.go`'s `RunE` turn errors into a non-zero exit via Cobra.
- Cleanup: functions that create temp resources (e.g. `downloadToTemp`) return a `func()` cleanup alongside the value/error — call it with `defer` at the call site. Follow this pattern for any new resource-creating function rather than inventing a new one.
- Output: user-facing progress messages go to stdout via `fmt.Printf`; don't introduce a logging framework for a CLI this size.
- Naming: exported Go-style `PascalCase` for functions meant to be reused across files in `package main` (as already done), even though nothing is a public API outside this module.
- Grading: any code path that decides whether a lab passes must go through `Proctor.Grade` (`proctor.go`) — don't add ad hoc `kubectl`/check logic directly in a `cmd_*.go` file. This is the trust boundary the "student submits, doesn't self-grade" model depends on.

## Build & run

```sh
go build -o astrona .
go vet ./...
go test ./...
```

Requires `kind` and either `docker` or `podman` on `PATH` to actually run `astrona run` / `astrona destroy` on the (default) `kind` runtime — these are shelled out to, not vendored.

For labs using the `qemu` runtime (`runtime.type: qemu`), also requires: `qemu-system-x86_64` and/or `qemu-system-aarch64`, `qemu-img`, `ssh`/`ssh-keygen`, and one ISO9660 tool (`mkisofs`/`genisoimage`/`xorriso` on Linux, `hdiutil` on macOS). On macOS: `brew install qemu cdrtools`. An `arch: aarch64` guest additionally needs edk2/AAVMF UEFI firmware on disk — `locateAArch64Firmware` in `hypervisor.go` checks common install paths (Homebrew's `qemu` formula bundles it) or `ASTRONA_QEMU_AARCH64_FIRMWARE` as an override.

## When making changes

- Don't add abstractions, config options, or defensive code for scenarios this tool doesn't need to handle (it's a local dev tool, not a hosted service).
- Do flag and fix real security gaps in the areas above when you touch them, even if not explicitly asked — but don't scope-creep a small fix into a rewrite.
- Keep error messages actionable for a developer running this interactively in a terminal.
