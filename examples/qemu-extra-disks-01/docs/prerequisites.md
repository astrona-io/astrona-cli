# Prerequisites: qemu-extra-disks-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.prerequisites`.

Same `qemu` runtime and toolchain as
[`qemu-basics-01`](../../qemu-basics-01/docs/prerequisites.md):
`qemu-system-x86_64`/`qemu-system-aarch64`, `qemu-img`, `ssh`/`ssh-keygen`,
`oras`, and one ISO9660 tool (`mkisofs`/`genisoimage`/`xorriso`/`hdiutil`).
`astrona check` reports what's missing.

This lab needs nothing extra on the host — the blank extra disks
(`runtime.qemu.extraDisks`) are created with the same `qemu-img` binary
already required for the main disk.

Ready? `astrona run -c examples/qemu-extra-disks-01`.
