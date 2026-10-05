# Runtimes: kind vs qemu

A lab's `runtime.type` picks which backend astrona spins up. Every command (`run`, `submit`, `destroy`, `test`) works the same way regardless of which one a lab uses — they all resolve to the same `LabEnvironment` shape internally.

```yaml
runtime:
  type: kind   # default — omit the whole `runtime:` block for the same effect
```

## `kind` (default)

A local Kubernetes cluster via [kind](https://kind.sigs.k8s.io/), on whichever container engine (Docker or Podman) astrona finds first on your `PATH`. This is the default and needs no `runtime:` block at all — every pre-existing lab config with no `runtime.type` keeps working unchanged.

- `bootstrap.manifests` / `testing.manifests` apply against the cluster via `kubectl --context kind-<cluster-name>`.
- `validation.checks` of type `resourceExists`/`podReady` run against the same context.
- Scripts (`bootstrap.init`, `teardown.init`, `validation.script`) run on the **host** — there's no VM to SSH into.
- `runtime.portForwards` exposes in-cluster services on `127.0.0.1` — see [Port forwards](#port-forwards).

### Port forwards

A kind lab can declare [`runtime.portForwards`](../reference/lab-config.md#runtimeportforwardsn) so a student reaches in-cluster services from their own browser or client, without running `kubectl port-forward` themselves:

```yaml
runtime:
  portForwards:
    - name: web
      resource: svc/frontend
      hostPort: 8080
      targetPort: 80
      scheme: http
```

`astrona run` starts them last, after bootstrap scripts and manifests, waits up to 30s for each to become ready, and prints how to reach them:

```text
Port forwards (bound to 127.0.0.1 only):
    web   Ready      http://127.0.0.1:8080   ->  svc/frontend:80 (ns default)    Frontend UI
    db    NotReady   tcp://127.0.0.1:5432    ->  svc/postgres:5432 (ns data)

  Not ready yet (still retrying in the background):
    db: error: unable to forward port because pod is not running. Current status=Pending
```

**How it works.** Each forward gets its own detached supervisor process (a hidden `astrona port-forward supervise`), which runs `kubectl --context kind-<lab> port-forward --address 127.0.0.1 …` and restarts it with backoff (1s doubling up to 10s) whenever it exits — plain `kubectl port-forward` dies whenever the pod behind it restarts. State lives in `~/.astrona/portforward/<lab>/<name>/` (`spec.json`, `status.json`, `supervisor.pid`, `supervisor.log` — check the log when a forward won't come up).

**Status** (`astrona port-forward list`):

| Status | Meaning |
|---|---|
| `Ready` | kubectl reported `Forwarding from …` **and** the local port accepts a TCP connection right now |
| `NotReady` | Supervisor running, kubectl not forwarding yet/again — pod not running, no endpoints, restart backoff |
| `Error` | The same non-transient kubectl failure 3+ times in a row (e.g. `services "x" not found`) — still retrying, likely needs a fix. "Pod not running yet" and "lost connection to pod" stay `NotReady` however long they last |
| `Stopped` | Supervisor not running (stopped, killed, reboot, or the cluster was deleted) — `astrona port-forward start -c <config>` |

`Ready` proves kubectl is listening on the host side, not that the application in the pod answers.

**Security.** Forwards always bind `127.0.0.1` — the address can't be set from the config, so a lab config fetched from a URL or git repo can never expose the cluster to your network. Host ports below 1024 are rejected, and `resource`/`namespace` are validated as Kubernetes names before they reach kubectl (passed as an argument list, never through a shell).

**Lifecycle.**

- `astrona run` — starts them (replacing any already running for the lab). A forward that fails or isn't ready yet only warns; the lab is still up.
- `astrona port-forward list|start|stop` — inspect, (re)start after a reboot, stop. See the [CLI reference](../reference/cli/astrona_port-forward.md).
- `astrona destroy` — stops them before deleting the cluster. With `teardown.keepCluster: true` they keep running too.
- `astrona test` — never starts them (CI has no browser, and they'd clash on host ports with a real `run`).

## `qemu`

Boots one or more full virtual machines from a base image instead of a container-backed cluster — for labs that need a real OS: kernel modules, systemd, package managers, multi-host networking, anything a container can't give you.

```yaml
runtime:
  type: qemu
  qemu:
    - image:
        type: url          # "file" | "url" | "oci"
        source: "https://cloud-images.ubuntu.com/.../noble-server-cloudimg-amd64.img"
        checksum: "sha256:..."   # optional but strongly recommended
      arch: amd64
      cpus: 2
      memoryMB: 2048
      diskSizeGB: 20
```

Key differences from `kind`:

- There's no `KubeContext` — `bootstrap.manifests`/`testing.manifests` are rejected with an error if set, since there's no kubectl-reachable cluster.
- Every script (`bootstrap.init`, `teardown.init`, `validation.script`) runs **inside the VM over SSH**, as a dedicated `astrona` superuser account (passwordless sudo, key-auth only) that astrona provisions on every VM — separate from the human-facing `student` account. Each account gets its own ephemeral ed25519 keypair, generated and removed on teardown. Script content is piped over stdin to `bash -s` — never interpolated into a shell string.
- `astrona ssh <lab-name>` opens an interactive session into a running VM (name as shown by `astrona list`, with or without the `astro-` prefix) as `student` by default — override with `--user`. Root SSH login is disabled on the VM. After `astrona run` finishes, a **Connect:** block prints the ready-to-paste `astrona ssh <name>` command for each VM.
- `student` can be locked down (sudo removed, password auth disabled) without affecting bootstrap/testing/teardown, since those always run as the independent `astrona` account.
- Base images are cached under `~/.astrona/cache/images` — inspect with `astrona images list`. Checksum verification is strongly recommended (`image.checksum`/`image.checksums`) but not required; an unverified image falls back to an online freshness check plus the existing cache.

### Image sources

`image.type` is one of:

| Type | `source` is | Notes |
|---|---|---|
| `file` | a local path (relative to the lab config's base directory) | no download, no freshness check |
| `url` | an `http(s)://` URL to a `.qcow2`/cloud image | downloaded and cached; `checksum`/`checksums` recommended |
| `oci` | an OCI registry reference (e.g. `ghcr.io/...`) | pulled via [`oras`](https://oras.land/) |

### Single VM vs multi-VM

`runtime.qemu` is always a list. A single-VM lab is a one-element list whose entry has no `name` — the original shape every qemu lab used. Add a second entry (each one now named) to make it a multi-VM lab:

```yaml
runtime:
  type: qemu
  networks:
    - name: internal
      cidr: 10.10.0.0/24
  qemu:
    - name: jumphost
      image: { type: url, source: "...", checksum: "sha256:..." }
      networks:
        - { name: internal, ipv4: 10.10.0.10 }
      sshAccess: [backend]     # passwordless SSH from jumphost into backend
    - name: backend
      image: { type: url, source: "...", checksum: "sha256:..." }
      networks:
        - { name: internal, ipv4: 10.10.0.11 }
```

In a multi-VM lab:

- Every VM gets an implicit host-only management NIC (astrona's own control channel) in addition to any declared `networks`.
- `sshAccess` wires up passwordless SSH from one named VM into another — both must already share a `runtime.networks` segment; it doesn't create connectivity on its own, only trust over a path that already exists.
- The lab's shared root `bootstrap`/`validation` run once per VM, in `runtime.qemu`'s order; a VM's own nested `bootstrap`/`validation` block (if set) runs after that, scoped to just that VM.

## Choosing one

Default to `kind` unless a lab specifically needs a full OS, kernel-level behavior, or multi-host networking that a Kubernetes cluster can't model — it's faster to boot and has no VM image to manage.
