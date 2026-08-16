# Prerequisites: qemu-jumphost-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.prerequisites`.

Same runtime, same base image, same tools on `PATH` as
[`qemu-basics-01`](../../qemu-basics-01) — see that lab's
[prerequisites.md](../../qemu-basics-01/docs/prerequisites.md) for the full
list: `qemu-system-*`, `qemu-img`, `ssh`, `oras`, an ISO9660 tool. No new
binary is required for the multi-NIC networking this lab uses — it's built
entirely from `-netdev socket,listen=.../connect=...`, a plain loopback TCP
socket, not a bridge/tap (which would need root).

## Why this lab exists

[`qemu-multi-01`](../../qemu-multi-01) shows multiple VMs in one lab, but
its own docs are explicit that its VMs **cannot** reach each other — every
VM there only gets the default host-only "user" (SLIRP) NIC, a private NAT
segment per VM. This lab is what changes once `runtime.networks` +
`runtime.qemu[].networks` exist: VMs can now share a named virtual network
segment, and one VM can straddle more than one.

## The topology

```
  client (10.10.1.10) --client-net-- jumphost --server-net-- server (10.20.1.10)
                                    (10.10.1.1)  (10.20.1.1)
```

- **client** has one extra NIC, on `client-net`.
- **server** has one extra NIC, on `server-net`.
- **jumphost** has two extra NICs — one on each segment — because it lists
  two entries under its own `networks:`.

client and server never list a segment in common, so there is no path
between them at all: not a firewall rule, an actual absence of a shared
NIC. jumphost is the only VM on both segments, so it's the only one that
can reach both — the "a server is only reachable through a jump host" shape
a real bastion-host setup has.

## How it works under the hood

Every VM always gets an implicit NIC (`net0`, qemu's `user`/SLIRP backend,
host-forwarded for SSH) — `networks:` doesn't replace that, it adds NICs on
top of it. This one isn't optional and there's no config knob to remove it:
it's astrona's only control channel into the VM (`astrona ssh`, bootstrap,
validation, `submit`, `destroy` all go through it), so a VM without it
would be unmanageable by astrona itself, not just more "realistic." It's
also why `server` here — which never lists `client-net` at all — still
shows up in `astrona list` with an SSH port: that's the mgmt NIC, entirely
separate from the `server-net` segment `server-net` actually cares about.

`runtime.networks` declares each segment's name and CIDR range
once, lab-wide; a VM joins one by listing that same `name` under its own
`networks:` with a bare `ipv4:` address (no `/prefix` — inherited from the
segment's declared `cidr`) inside that range.

Each `networks:` entry becomes an additional
`-netdev socket,listen=127.0.0.1:<port>` (or `connect=`, depending on
role) — a loopback TCP socket, not multicast. Multicast was the first
design tried here (a shared UDP group any number of VMs could join at
once), but it doesn't reliably deliver over loopback on every host this
tool targets, so it's not what shipped: `resolveNetworkTopology` in
`hypervisor.go` instead requires each segment to have exactly two VMs, and
assigns whichever one declares it first (lab-wide) as the TCP listener and
the other as the connector, on a port both derive the same deterministic
way (`deriveNetworkPort`, keyed by the lab's own name so two unrelated labs
both calling a segment `"server-net"` never collide). Since
`CreateEnvironment` boots VMs one at a time, in that same declaration
order, the listener's socket is always already open — set up at qemu
process launch, long before that VM's own guest OS finishes booting — by
the time any later VM's connector tries to reach it.

`ipv4:` gets combined with the segment's declared prefix length and
written into the VM's cloud-init `network-config`, matched to the right
NIC by a MAC address `resolveNetworkTopology` derives the same
deterministic way (so it's stable across reboots without needing to guess
interface names).

## What gets rejected before any VM boots

- A VM's `networks:` entry naming a segment `runtime.networks` never
  declared.
- A VM's `ipv4:` falling outside its segment's declared `cidr`.
- Two `runtime.networks` entries with overlapping `cidr` ranges.
- Any segment joined by anything other than exactly two VMs (astrona's
  point-to-point backend can't do a three-way shared segment).

## Running it

```sh
astrona run -c examples/qemu-jumphost-01
astrona list                                   # NICS column shows 2 or 3 per VM, with segment=IP
astrona ssh qemu-jumphost-01-jumphost
astrona submit -c examples/qemu-jumphost-01    # grades all three VMs' connectivity
astrona destroy -c examples/qemu-jumphost-01
```

See [`../README.md`](../README.md) for a fuller walkthrough.
