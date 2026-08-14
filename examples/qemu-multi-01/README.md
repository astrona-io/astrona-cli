# qemu-multi-01 — multiple VMs in one lab

Demonstrates a multi-VM qemu lab: a single `astrona run` that boots more
than one VM for the same lab, plus shared ("runs on every VM") vs. per-VM
bootstrap/validation. See [`docs/prerequisites.md`](docs/prerequisites.md)
first, especially the note on why the two VMs can't reach each other over
the network — this lab proves both were provisioned correctly, not that
they can talk to each other.

## The schema, compared to qemu-basics-01

`runtime.qemu` is always a list. Compare
[`config.yaml`](config.yaml) with
[`../qemu-basics-01/config.yaml`](../qemu-basics-01/config.yaml):
`qemu-basics-01` has exactly **one** list entry with **no `name:`** — that's
what makes it single-VM. This lab has **two** named entries (`server`,
`client`), so it's multi-VM. Extending a single-VM lab into a multi-VM one
is just appending another list entry, not restructuring the block.

Bootstrap/validation placement decides *where* something runs:

- **Root** `bootstrap:`/`validation:` (top-level, same place `qemu-basics-01`
  uses them) — since `runtime.qemu` here names more than one VM, these run
  **once per VM** instead of just once: shared setup/checks every VM needs.
- **Nested** under a `runtime.qemu[]` entry's own `bootstrap:`/`validation:`
  — runs **only on that VM**, after the shared root ones.

## 1. Build and check

```sh
go build -o astrona .
./astrona check
```

## 2. Bring both VMs up

```sh
./astrona run -c examples/qemu-multi-01
```

Order per VM: the shared root `common-setup.sh` first, then that VM's own
nested bootstrap script (`server-setup.sh` or `client-setup.sh`):

```
 [1/1] Init: common-setup
common setup running on: qemu-multi-01-server
 [1/1] Init: server-setup
hello from the server VM: qemu-multi-01-server
 [1/1] Init: common-setup
common setup running on: qemu-multi-01-client
 [1/1] Init: client-setup
hello from the client VM: qemu-multi-01-client
```

## 3. See both VMs

```sh
./astrona list
```

```
qemu VMs:
  ●  qemu-multi-01-client     pid=42726    ssh=student@127.0.0.1:61996  uptime=23s
  ●  qemu-multi-01-server     pid=42642    ssh=student@127.0.0.1:61975  uptime=38s
```

Each VM's name is `<lab name>-<vm name>` — an ordinary qemu lab name as far
as `astrona list`/`astrona ssh`/`astrona destroy` are concerned, no special
multi-VM handling needed in any of them.

## 4. SSH into each one

```sh
./astrona ssh qemu-multi-01-server
cat /tmp/astrona-lab/common-marker.txt   # common-ready (from the shared root bootstrap)
cat /tmp/astrona-lab/server-marker.txt   # server-ready (from server's own nested bootstrap)
exit
```

## 5. Grade both

```sh
./astrona submit -c examples/qemu-multi-01
```

```
  PASS  verify-common-marker (server) (0.06s)
  PASS  verify-server-marker (server) (0.06s)
  PASS  verify-common-marker (client) (0.06s)
  PASS  verify-client-marker (client) (0.06s)

4 passed, 0 failed in 0.23s

PROCTOR: PASS
```

The shared root `validation.script` (`validate-common.sh`) runs once per
VM — each gets its own row, suffixed `(server)`/`(client)` since it's
checking each VM's own independent state, not one shared result. Each VM's
own nested `validation.script` (`validate-server.sh`/`validate-client.sh`)
runs alongside it.

## 6. Tear down both

```sh
./astrona destroy -c examples/qemu-multi-01
./astrona list   # both gone
```

One `astrona destroy` tears down every VM in `runtime.qemu` — best-effort
per VM, so one failing to stop doesn't block the other from being cleaned
up.

## What's actually new here (for anyone extending astrona)

- `RuntimeConfig.QEMU []QEMUVM` (`config.go`) — `runtime.qemu` is always a
  list now; `isMultiVM`/`validateQEMUVMs` decide single- vs. multi-VM from
  it (named entries + count, not a separate flag).
- `QEMUVM.Bootstrap`/`QEMUVM.Validation` (`config.go`) — the per-VM nested
  override blocks.
- `LabEnvironment.Executors` + `executorForVM` (`runtime.go`) — one SSH
  executor per VM; `Executor` (singular) stays nil for a multi-VM lab so
  orchestration code can always tell single- from multi- by which field is
  set.
- `runBootstrap`/`runOnEveryVM` (`scripts.go`) — run a script list once via
  `env.Executor`, or once per VM via `env.Executors`; `runBootstrap` also
  layers in each VM's own nested `Bootstrap.Init` afterward.
- `Proctor.gradeScripts`/`runValidationBlock` (`proctor.go`) — the same
  shared-vs-per-VM logic for validation, producing one `(vmName)`-suffixed
  result per VM for a shared script.
- `ValidationConfig.Scripts` (plural, `config.go`) — additive alongside the
  older singular `Script`; not required for this lab (each `validation:`
  block here only needs one script) but available if a VM needs to run more
  than one.

None of `astrona list`, `astrona ssh`, `cmd_check.go`, or the qemu process
lifecycle itself (`hypervisor.go`'s `CreateQEMUVM` et al.) needed to
change — every VM just looks like an independent, ordinarily-named qemu lab
to all of them.
