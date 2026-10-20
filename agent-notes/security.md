# Security reference

## Security-sensitive areas — extra care required

- **Remote script execution** (`RunInitScripts` in `internal/scripts/scripts.go`, `DownloadToTemp` in `internal/config/download.go`): bootstrap/testing/teardown entries of type `url` are downloaded over HTTP(S) and executed with `bash`. Prefer HTTPS-only, enforce size limits on downloads, and never execute anything without an explicit, auditable code path.
- **QEMU base images** (`acquireBaseImage` in `internal/hypervisor/hypervisor.go`): VM base images are full bootable OSs. Checksum verification is strongly recommended but optional (warns when unset). Unverified images use an online freshness check (`checkURLFreshness`, `checkOCIFreshness`) and cache fallback.
- **QEMU SSH access** (`SSHExecutor` in `internal/executor/executor.go`, `CreateQEMUVM`/`buildCloudInitSeed` in `internal/hypervisor/hypervisor.go`): each VM gets two accounts, each with its own ephemeral ed25519 keypair generated and removed on teardown. `student` (`qemuSSHUser`) is the human-facing account `astrona ssh` connects as. `astrona` (`adminSSHUser`) is a dedicated superuser the CLI itself uses to run init/bootstrap/testing/teardown scripts (`sshExecutorFor` in `internal/runtime/runtime.go` always builds the `SSHExecutor` from the admin identity, never the student one) — kept independent so a lab can lock down `student` later (drop its sudo, disable its password auth) without breaking the CLI's own automation. Root SSH login stays disabled (`disable_root: true`). Script content is piped over stdin to `bash -s` to prevent interpolation.
- **Remote config fetch** (`LoadLabConfig` in `internal/config/config.go`): lab config YAML can be fetched from an arbitrary URL. Enforce standard parsing safety.
- **Git config source** (`internal/gitsource/gitsource.go`): `--git <url>` clones/pulls an arbitrary repo URL under `gitCacheDir`, running force checkouts and cleans to ensure security.
- **Path handling** (`ResolveConfigPath` in `internal/config/config.go`, `JoinWithinBaseDir` in `internal/config/path.go`): relative paths are strictly resolved against the config base directory and rejected if they escape baseDir.
- **Subprocess execution** (`os/exec` calls to `kind`, `docker`/`podman`, `bash`): always use `exec.Command` with argument slices.

