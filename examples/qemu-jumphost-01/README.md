# qemu-jumphost-01 — multi-NIC VMs and a jump-host topology

Demonstrates `runtime.networks` + `runtime.qemu[].networks`: declaring named
virtual network segments and attaching more than one NIC to a qemu VM, used
here to build a topology where one VM (a "jump host") straddles two
otherwise-disconnected segments. Three VMs:

```
  client (10.10.1.10) --client-net-- jumphost --server-net-- server (10.20.1.10)
                                    (10.10.1.1)  (10.20.1.1)
```

`client` and `server` never share a segment — there's no path between them
at all. `jumphost` has a NIC on both, so it's the only VM that can reach
either one directly. See [`docs/prerequisites.md`](docs/prerequisites.md)
for how the segments are actually built (loopback TCP sockets, no
root/bridge needed).

## The schema, compared to qemu-multi-01

[`qemu-multi-01`](../qemu-multi-01) proved multiple VMs can boot from one
`astrona run` — but its docs are explicit that those VMs can't talk to each
other at all. `runtime.networks` + `networks:` is what closes that gap.
[`config.yaml`](config.yaml):

```yaml
runtime:
  type: "qemu"
  networks:                       # declared once, lab-wide
    - name: "client-net"
      cidr: "10.10.1.0/24"
    - name: "server-net"
      cidr: "10.20.1.0/24"
  qemu:
    - name: "client"
      networks:
        - name: "client-net"
          ipv4: "10.10.1.10"      # no /prefix — inherited from client-net's cidr above

    - name: "jumphost"
      networks:
        - name: "client-net"
          ipv4: "10.10.1.1"
        - name: "server-net"
          ipv4: "10.20.1.1"
```

`runtime.networks` declares each segment's name and CIDR range once, up
front — a VM joins one by listing the same `name` under its own `networks:`
with a bare `ipv4:` address inside that range; no `/prefix` on the VM side,
since it's already implied by the segment's declared `cidr`. Two entries
under `jumphost`'s own `networks:` is the entire difference between a
single-homed VM and a multi-homed one — every VM still also gets its usual
implicit host-only NIC for `astrona ssh`, regardless of this list.

Referencing a segment `jumphost` never declared at the top, giving a VM an
`ipv4:` outside that segment's declared range, or declaring two segments
with overlapping ranges, are all rejected before any VM boots — try, e.g.,
changing `client`'s `ipv4:` to something outside `10.10.1.0/24` and running
`astrona run`; you'll get a config error naming exactly which VM and
segment, not a VM that boots and silently can't be reached.

## 1. Build and check

```sh
go build -o astrona .
./astrona check
```

## 2. Bring all three VMs up

```sh
./astrona run -c examples/qemu-jumphost-01
```

The `Networks: client-net=10.10.1.0/24, server-net=10.20.1.0/24` line near
the top of the output is `runtime.networks` echoed back — the segments this
run is actually using, always printed so they're never just buried in
`config.yaml`. Each VM's `nics=` count further down shows the implicit mgmt
NIC plus however many `networks:` entries it has — 2 for `client`/`server`,
3 for `jumphost`.

## 3. See all three VMs, with their NICs

```sh
./astrona list
```

```
NAME                              RUNTIME   STATUS    UPTIME   NICS                                                        DETAILS
astro-qemu-jumphost-01-client     qemu      Running   0m14s    2 (mgmt, client-net=10.10.1.10/24)                         ssh student@127.0.0.1 -p 61955
astro-qemu-jumphost-01-jumphost   qemu      Running   0m22s    3 (mgmt, client-net=10.10.1.1/24, server-net=10.20.1.1/24) ssh student@127.0.0.1 -p 61974
astro-qemu-jumphost-01-server     qemu      Running   0m8s     2 (mgmt, server-net=10.20.1.10/24)                         ssh student@127.0.0.1 -p 61986
```

The `NICS` column spells out `mgmt` by name — the implicit, always-present
host-only NIC astrona itself uses for `astrona ssh`/bootstrap/validation,
not part of this lab's own topology — followed by every extra NIC's
segment name and the static IP that VM has on it. `mgmt` is why every VM
here has one more NIC than its own `networks:` list would suggest on its
own; it's deliberate plumbing (astrona's only control channel into the VM),
not something a lab config turns off.

## 4. Prove the topology by hand

```sh
./astrona ssh qemu-jumphost-01-jumphost
ping -c1 10.10.1.10   # -> client, reachable (client-net)
ping -c1 10.20.1.10   # -> server, reachable (server-net)
exit

./astrona ssh qemu-jumphost-01-client
ping -c1 10.10.1.1    # -> jumphost, reachable (client-net)
ping -c1 10.20.1.10   # -> server, NOT reachable — no shared segment
exit
```

## 5. Prove the trust half, too: `ssh client` / `ssh server` from jumphost

```sh
./astrona ssh qemu-jumphost-01-jumphost
ssh client hostname   # no password prompt, no manual key setup
ssh server hostname   # same
exit
```

This is what `sshAccess` on `jumphost` in [`config.yaml`](config.yaml) buys
over the network segmentation alone — the two ping checks above prove
jumphost *can reach* client/server; this proves it can also *log into* them,
with no manual key copying:

```yaml
    - name: "jumphost"
      networks: [...]
      sshAccess:
        - "client"
        - "server"
```

astrona generates one dedicated ed25519 keypair for jumphost — never the
same key astrona itself uses for `astrona ssh` into any VM, and never
written to the host's disk at all — embeds the private half straight into
jumphost's own cloud-init seed, and appends the public half to client's and
server's `ssh_authorized_keys`. It also writes a `~/.ssh/config` Host entry
per target inside jumphost, which is why `ssh client` (not
`ssh student@10.10.1.10 -i ...`) is enough. `validate-jumphost.sh`'s last
two checks assert exactly this — a `sshAccess` typo or a target with no
shared network segment fails config validation before any VM boots (see
`ResolveInterVMTrust`'s error messages), not silently at grading time.

A real SSH jump — `ssh -J jumphost server` from the host itself — would
still need the host's own key trusted by both hops; `sshAccess` wires up
trust *between* VMs in the lab, not from the host through them.

## 6. Grade all three

```sh
./astrona submit -c examples/qemu-jumphost-01
```

```
  PASS  verify-client-network (client) (2.1s)
  PASS  verify-jumphost-network (jumphost) (0.3s)
  PASS  verify-server-network (server) (2.1s)

3 passed, 0 failed in 4.5s

PROCTOR: PASS
```

`verify-client-network`/`verify-server-network` each ping their
expected-reachable neighbor (must succeed) and their expected-unreachable
one (must fail) — so a config mistake that accidentally bridges `client`
and `server`, or fails to bridge `jumphost` to one of them, fails grading
instead of going unnoticed. `verify-jumphost-network` additionally runs
`ssh client hostname` and `ssh server hostname` from jumphost, so a
`sshAccess` regression (a stale/wrong `authorized_keys` entry, a broken
`~/.ssh/config` alias) fails grading the same way.

## 7. Tear down all three

```sh
./astrona destroy -c examples/qemu-jumphost-01
./astrona list   # all three gone
```

## What's actually new here (for anyone extending astrona)

- `QEMUNetworkDef`/`RuntimeConfig.Networks` (`config.go`/`hypervisor.go`) —
  the top-level `runtime.networks` schema: a segment `name` and its `cidr`.
- `QEMUNetwork`/`QEMUVM.Networks` — the per-VM `networks:` schema: a segment
  `name` (must match a `runtime.networks` entry) and a bare `ipv4:` address
  (must fall inside that segment's declared `cidr`; no `/prefix` — that's
  inherited from there).
- `resolveNetworkTopology` (`hypervisor.go`) — the whole-lab pass that
  validates all of the above (undeclared segment name, out-of-range `ip:`,
  overlapping declared `cidr`s, a segment with anything other than exactly
  2 VMs) and assigns each segment's two VMs a loopback TCP listen/connect
  role plus a shared rendezvous port (`deriveNetworkPort`) — called once by
  `CreateEnvironment` before any VM boots, since role assignment needs to
  see every VM in the lab at once. `deriveMAC` derives each NIC's stable
  MAC the same deterministic way.
- `buildQEMUArgs` — an extra `-netdev socket,listen=127.0.0.1:<port>` (or
  `connect=`, depending on role) + `-device virtio-net-pci` pair per
  `networks:` entry, alongside the always-present mgmt NIC (`net0`). A
  plain loopback TCP socket, not multicast — multicast was tried first but
  doesn't reliably deliver on loopback on every host this tool targets, so
  it's not what shipped.
- `buildCloudInitSeed` — writes a cloud-init `network-config` (NoCloud,
  netplan v2) matching each NIC by MAC, only when a VM has at least one
  extra NIC — a VM with none keeps cloud-init's untouched default behavior.
- `QEMUHandle.Networks`/`QEMUNetworkStatus` — persisted per VM so a
  separate process (`astrona list`) can show NIC/IP info without
  re-reading `config.yaml`.
- `cmd_list.go`'s `NICS` column.
- `QEMUVM.SSHAccess`/`ResolveInterVMTrust` (`config.go`/`hypervisor.go`) —
  the `sshAccess:` schema and the whole-lab pass behind it: for each source
  VM's `sshAccess` entries, generates one dedicated ed25519 keypair
  (`generateInMemorySSHKeyPair` — never written to the host's disk, unlike
  the per-VM host-access key `generateEphemeralSSHKey` already generates),
  validates the source and target actually share a `runtime.networks`
  segment (`sharedSegmentIP`), and returns each VM's `InterVMTrust` —
  called once by `createMultiQEMUEnvironment`, same call-before-any-VM-boots
  reasoning as `resolveNetworkTopology`, since a target's
  `authorized_keys` must already carry its source's public key by the time
  cloud-init runs on that target's first boot.
- `buildCloudInitSeed`'s `trust` parameter — appends each `InterVMTrust`'s
  extra public keys to that VM's `ssh_authorized_keys:`, and, only for a
  VM that's itself a `sshAccess` source, an appended `runcmd:` block
  (`interVMRuncmd`) installing its private key plus a `~/.ssh/config` Host
  alias per target. A `runcmd`, deliberately not `write_files`: cloud-init's
  default module order runs `write_files` *before* the `users`/`ssh`
  modules that create the guest user and its `$HOME`.

Because astrona's networking backend is point-to-point (a loopback TCP
socket per segment, not a shared multi-party one), a segment may only ever
have exactly two VMs — `resolveNetworkTopology` rejects a third VM joining
`client-net` or `server-net` the same way it rejects an undeclared segment
name. A "jump host" topology like this one is built from two point-to-point
segments meeting at one VM, not one three-party segment.
