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

This prints a ✓/⚠/✗ per dependency and exits non-zero only if a *required* one is missing — optional ones (qemu toolchain, `git`) only warn, since they're only needed for specific runtimes or flags.

## Staying up to date

```sh
astrona upgrade
```

Checks GitHub for the latest release, downloads the binary for your OS/architecture, and atomically replaces the currently running executable. Astrona also nudges you on startup (`[INFO] A new version of astrona is available...`) whenever a newer release exists.

## Next

Continue to the [Quickstart](quickstart.md) to run your first lab.
