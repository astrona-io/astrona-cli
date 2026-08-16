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

(A real SSH jump — `ssh -J jumphost server` — would additionally need
`client`'s public key trusted by `server`, which this lab doesn't set up:
it's demonstrating the network segmentation a jump host relies on, not
building a full bastion-host auth chain. A lab that wanted that could add
it in `client`'s bootstrap, copying its own pubkey into `jumphost`'s and
`server`'s `authorized_keys`.)

## 5. Grade all three

```sh
./astrona submit -c examples/qemu-jumphost-01
```

```
  PASS  verify-client-network (client) (2.1s)
  PASS  verify-jumphost-network (jumphost) (0.1s)
  PASS  verify-server-network (server) (2.1s)

3 passed, 0 failed in 4.2s

PROCTOR: PASS
```

Each VM's validation script pings its expected-reachable neighbor (must
succeed) and its expected-unreachable one (must fail) — so a config
mistake that accidentally bridges `client` and `server`, or fails to bridge
`jumphost` to one of them, fails grading instead of going unnoticed.

## 6. Tear down all three

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

Because astrona's networking backend is point-to-point (a loopback TCP
socket per segment, not a shared multi-party one), a segment may only ever
have exactly two VMs — `resolveNetworkTopology` rejects a third VM joining
`client-net` or `server-net` the same way it rejects an undeclared segment
name. A "jump host" topology like this one is built from two point-to-point
segments meeting at one VM, not one three-party segment.
