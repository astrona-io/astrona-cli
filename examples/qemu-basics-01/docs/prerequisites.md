# Prerequisites: qemu-basics-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.prerequisites` — read this before attempting the [exam question](./exam-question.md).

This lab runs on the `qemu` runtime instead of `kind` — it boots a real VM
with `qemu-system-x86_64` rather than a container-based Kubernetes cluster.
No `bootstrap.manifests`/`validation.checks` (kubectl) are used; the whole
lab is bootstrap init + a validation script, both run inside the VM over
SSH.

## Tools on PATH

- `qemu-system-x86_64`, `qemu-img` — the VM itself and its disk tooling
- `ssh`, `ssh-keygen` — used to provision and exec into the VM
- `oras` — pulls the base image (published as an OCI artifact) referenced by `config.yaml`
- One ISO9660 tool: `mkisofs`, `genisoimage`, `xorriso`, or `hdiutil` (macOS builtin) — used to build the cloud-init seed image

On macOS: `brew install qemu cdrtools oras` gets you `qemu-system-x86_64`,
`qemu-img`, `mkisofs`, and `oras` in one go; `ssh`/`ssh-keygen`/`hdiutil` are
already on the system.

## Base image

`config.yaml`'s `runtime.qemu.image` points at a `cloud-init`-enabled qcow2
image published as an OCI artifact in
[astrona-io/qcow2-base-image](https://github.com/astrona-io/qcow2-base-image)
(GitHub Container Registry). `astrona run` pulls it automatically via
`oras pull` — nothing to download by hand. `image.source` uses the literal
`{ARCH}` placeholder (see [`README.md`](README.md)'s "Multi-arch images"
section) so the right tag gets pulled for whichever host runs it.

`image.checksums` is set and checked before the VM ever boots off the pulled
image — but checksum verification is optional, not enforced by astrona (see
`CLAUDE.md`'s security-sensitive-areas notes): omit `checksum`/`checksums`
entirely and it still runs, just unverified, with a `[WARN]` on every run and
no caching for that image. If you push your own build:

```sh
oras push ghcr.io/astrona-io/ubuntu-qcow2-image:24.04-base-arm64 image.qcow2
shasum -a 256 image.qcow2
```

and update `runtime.qemu.image.checksums` (or `checksum`, if you're not
templating `{ARCH}`) with the resulting hex digest, prefixed with `sha256:`
— or copy it straight from `oras manifest fetch <ref>`'s layer digest,
since OCI blobs are content-addressed by that same value. Any
`type: "file"` or `type: "url"` source still works too, if you'd rather
point at a local image or a plain HTTPS URL.

## Running it

```sh
astrona run -c examples/qemu-basics-01
astrona submit -c examples/qemu-basics-01
astrona destroy -c examples/qemu-basics-01
```

`astrona run` returns as soon as the VM is SSH-ready and bootstrap has run —
it does not block, and the VM keeps running in the background afterward
(headless by default; set `runtime.qemu.display: true` in `config.yaml` if
you want an actual window). Two ways to check what's still running or get
into a VM directly:

```sh
astrona list                          # every qemu VM astrona knows about, with uptime
astrona ssh qemu-basics-01             # interactive shell inside this lab's VM (name, not --config)
```

Always `astrona destroy -c examples/qemu-basics-01` when you're done — a
qemu VM is a real background process astrona manages itself (unlike a kind
cluster, which `docker ps`/`kind get clusters` would also show you), so a
forgotten one won't show up anywhere else on the machine. `astrona list`
also flags any stale state left behind by a VM that already died on its own.

`config.yaml` leaves `runtime.qemu.arch` unset, so it defaults to *this
host's own* architecture and runs with native hardware acceleration
(HVF/KVM) rather than software emulation. That default currently only
matters in one direction, though: `image` is pinned to the one image
actually published — `ghcr.io/astrona-io/ubuntu-24.04-server-docker:arm64`
— so this lab only works on an Apple Silicon Mac or an arm64 Linux host
today. On an x86_64 host the guest arch would auto-detect to `x86_64`, but
there's no matching x86_64 image published yet to boot with it (that's a
mismatch qemu can't paper over the way it can fall back to slower TCG
emulation — wrong CPU architecture entirely, not just unaccelerated). See
[`README.md`](README.md)'s "Multi-arch images" section for how this config
is meant to pick up an `:amd64` build automatically once one exists.
aarch64 needs UEFI firmware installed (Homebrew's `qemu` formula already
bundles it; see `CLAUDE.md`'s "Build & run" section for other OSes).

Ready? Go to the [exam question](./exam-question.md).
