# Prerequisites: qemu-multi-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.prerequisites`.

This lab is the multi-VM sibling of
[`qemu-basics-01`](../../qemu-basics-01) — same runtime, same base image,
same tools on `PATH` (see that lab's
[prerequisites.md](../../qemu-basics-01/docs/prerequisites.md) for the full
list: `qemu-system-*`, `qemu-img`, `ssh`, `oras`, an ISO9660 tool).

The difference is `runtime.qemu`: two named entries (`server` and `client`)
instead of one unnamed one. Bootstrap/validation placement decides where
something runs — root-level (top of `config.yaml`, same as
`qemu-basics-01` uses) runs once per VM since there's more than one;
nested under a `runtime.qemu[]` entry's own `bootstrap:`/`validation:` runs
only on that VM. See [`../README.md`](../README.md) for the details.

## Important: the two VMs in *this* lab still can't reach each other

Each VM here only has qemu's implicit `-netdev user` (SLIRP) NIC — the same
NAT-to-host-only setup `qemu-basics-01` uses. That's an independent, private
network segment *per VM*, with a host-forwarded SSH port and nothing else.
The server VM and the client VM in this lab **cannot** `curl`/`ping`/SSH each
other directly — only the host (astrona itself, via SSH) can reach either
one. This lab doesn't attempt inter-VM networking: both VMs independently
prove they were bootstrapped correctly, which is what `runtime.qemu.vms`
itself is demonstrating.

That said, inter-VM networking **is** now something astrona's qemu runtime
supports — just not something this particular lab opts into. Declare a
segment under `runtime.networks` (name + CIDR range), then add a
`networks:` entry to two `runtime.qemu[]` VMs to give each one an extra NIC
on it; see [`../qemu-jumphost-01`](../qemu-jumphost-01), which uses that to
build a three-VM topology where a "jump host" VM bridges two
otherwise-disconnected segments.

## Running it

```sh
astrona run -c examples/qemu-multi-01
astrona list                              # two rows: qemu-multi-01-server, qemu-multi-01-client
astrona ssh qemu-multi-01-server
astrona ssh qemu-multi-01-client
astrona submit -c examples/qemu-multi-01  # grades both VMs
astrona destroy -c examples/qemu-multi-01 # tears down both
```

See [`../README.md`](../README.md) for a fuller walkthrough.
