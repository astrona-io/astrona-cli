# Runtimes: kind vs qemu

A lab's `runtime.type` picks which backend astrona spins up. Every command (`run`, `submit`, `destroy`, `test`) works the same way regardless of which one a lab uses — they all resolve to the same `LabEnvironment` shape internally.

```yaml
runtime:
  type: kind   # default — omit the whole `runtime:` block for the same effect
```

## `kind` (default)

A local Kubernetes cluster via [kind](https://kind.sigs.k8s.io/), on whichever container engine (Docker or Podman) astrona finds first on your `PATH`. This is the default and needs no `runtime:` block at all — every pre-existing lab config with no `runtime.type` keeps working unchanged.

### Cluster shape (`runtime.kind`)

By default a lab gets kind's defaults: one control-plane node, the node image bundled with the installed `kind`, and the kindnet CNI. A lab that needs more declares [`runtime.kind`](../reference/lab-config.md#runtimekind):

```yaml
runtime:
  kind:
    version: v1.31.2          # match the exam's Kubernetes version
    nodes:
      workers: 2              # scheduling, taints, drain, DaemonSets
    networking:
      disableDefaultCNI: true # bring your own CNI in bootstrap
      podSubnet: 192.168.0.0/16
```

astrona renders this into a kind cluster config file (a private temp file, removed afterwards) and runs `kind create cluster --config <file> --image <node image>`. The rendered config is printed in the run log (`astrona logs view`), and the step line shows the shape, e.g. `Create kind cluster "astro-my-lab" (podman; kindest/node:v1.31.2, 1 control plane + 2 workers, no default CNI)`.

Things to know:

- **Version vs. kind release.** `version: v1.31.2` boots `kindest/node:v1.31.2`. Node images are built per kind release, so pick one listed in the release notes of the `kind` version your students use — or pin `image:` by digest from those notes for a fully reproducible lab. If creation fails, the error names the image to check.
- **`disableDefaultCNI`.** Nodes stay `NotReady` and pods `Pending` until the lab's own `bootstrap` installs a CNI — that's expected, and it's exactly what a CNI lab wants the student (or bootstrap) to fix.
- **Resources.** Every node is a container. Two workers roughly triples the memory of a single-node lab — keep an eye on Podman/Docker Desktop VM limits. Caps: 3 control planes, 6 workers.
- **Not exposed, on purpose.** kind's own config can also bind-mount host directories into nodes (`extraMounts`), publish node ports on all interfaces (`extraPortMappings`), and patch kubeadm arbitrarily (`kubeadmConfigPatches`). A lab config can come from any URL or git repo, so astrona only accepts the typed fields above — a remote lab can shape its cluster, but can't use it to reach into the student's machine. (The only host port mapping astrona ever generates is the gateway addon's own, always on `127.0.0.1`.)

### Addons (`runtime.kind.addons`)

Common cluster components a lab can switch on instead of installing them in its own bootstrap scripts:

```yaml
runtime:
  kind:
    addons:
      cni: calico            # Calico v3.32.2 instead of kindnet — enforces NetworkPolicy
      certManager: true      # cert-manager v1.21.2
      metricsServer: true    # metrics-server v0.9.0 — kubectl top, HPA
      gatewayAPI: envoy      # Gateway API + Envoy Gateway v1.9.2, GatewayClass "eg"
```

They're installed right after the cluster is created and before `bootstrap`, in a fixed order (CNI first — nothing schedules without one — gateway last), each waited on until it's actually ready, so bootstrap scripts and manifests can rely on them. Adds roughly a minute per addon on a fresh machine, less once images are cached.

**Supply chain.** Each addon is pinned to one upstream release. The manifest's SHA-256 is compiled into astrona; it's downloaded over HTTPS once, verified, cached in `~/.astrona/cache/addons/<sha256>.yaml`, and re-verified on every use — a mismatch is never applied. A lab config chooses *which* addons, never *what* gets applied or from where. New versions arrive with astrona releases.

**Gateway API from the host browser.** With `gatewayAPI: envoy`, every Gateway with `gatewayClassName: eg` shares one Envoy deployment. Listeners on port **80** and **443** are reachable from your machine at `127.0.0.1:8080` / `127.0.0.1:8443` (change with `gatewayPorts`). `*.localtest.me` resolves to `127.0.0.1`, so an HTTPRoute with `hostnames: [web.localtest.me]` is at `http://web.localtest.me:8080`:

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: lab }
spec:
  gatewayClassName: eg
  listeners: [{ name: http, protocol: HTTP, port: 80 }]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: web }
spec:
  parentRefs: [{ name: lab }]
  hostnames: [web.localtest.me]
  rules: [{ backendRefs: [{ name: web, port: 80 }] }]
```

Under the hood astrona adds a NodePort Service (`envoy-gateway-system/astrona-gateway`, node ports 30080/30443) in front of the shared Envoy and has kind map those node ports to the host — on `127.0.0.1` only. Gateways report `Programmed`, so `waitFor: [{resource: gateway/lab, condition: Programmed}]` works. `astrona test` installs the same addons but skips the host port mapping, so it never clashes with a running `astrona run` of the same lab.

Classic `Ingress` isn't offered: ingress-nginx is retired upstream. Cilium isn't offered yet (it has no plain-manifest release).

- After `kind create cluster`, astrona waits until the `default` ServiceAccount exists before anything else runs — kind returns slightly before the cluster accepts Pods, which used to make a lab's first `kubectl apply` fail intermittently (`serviceaccount "default" not found`).
- `bootstrap.manifests` / `testing.manifests` apply against the cluster via `kubectl --context kind-<cluster-name>`.
- `validation.checks` of type `resourceExists`/`podReady` run against the same context.
- Scripts (`bootstrap.init`, `teardown.init`, `validation.script`) run on the **host** — there's no VM to SSH into — with `KUBECONFIG` set to the lab's own kubeconfig (see below), so a plain `kubectl` in a script always hits the lab.
- `runtime.portForwards` exposes in-cluster services on `127.0.0.1` — see [Port forwards](#port-forwards).

### Preloaded images (`runtime.kind.preloadImages`)

```yaml
runtime:
  kind:
    preloadImages:
      - nginx:1.27-alpine
      - busybox:1.36
```

Right after the cluster is created (before addons and bootstrap), each image is made available inside every node so pods start without pulling it:

1. If your container engine (Docker/Podman) doesn't have it yet, it's pulled once on the host — after that, repeat `astrona run`/`astrona test` need no network for it.
2. It's saved to a private temporary archive and loaded with `kind load image-archive` into every node.

Why: faster lab start (no per-node pulls), no Docker Hub rate limits in CI, and labs that work offline once the host has the images. Pod events show `Container image "…" already present on machine`.

Rules: every image needs an explicit tag or digest, never `:latest` or untagged — Kubernetes always re-pulls those (`imagePullPolicy: Always` by default), which would make preloading pointless. At most 30 images. Images are pulled for the host's architecture, which is the nodes' architecture too.

### Pausing a lab (`astrona stop` / `astrona start`)

```sh
astrona stop my-lab     # stops the node containers, pauses port forwards
astrona start my-lab    # starts them again, waits for the API, restarts port forwards
```

Stopping frees the CPU and memory the lab's node containers use without losing anything — the cluster, its workloads and its port forward definitions all come back on `start`. `astrona list` shows a paused lab as `Stopped`; `astrona destroy` works on it as usual. With no lab name, `stop` picks the only running kind lab and `start` the only stopped one (or the lab from `-c`).

Limits:

- **Single control plane only.** A lab with `nodes.controlPlanes` > 1 is refused: its node containers get new IP addresses on restart, and etcd's peer configuration is bound to the old ones, so the API never comes back. Use `astrona destroy` + `astrona run` for those.
- **kind labs only** for now; qemu labs aren't supported yet.
- Pods restart with their nodes, so anything not persisted (e.g. `emptyDir`, in-memory state) starts fresh.

### Kubeconfig isolation

Every kind lab gets its own kubeconfig, `~/.astrona/kind/<lab>/kubeconfig` (mode `0600`), containing only that cluster. astrona:

- **never changes your kubectl current-context.** `kind create cluster` switches it to the new cluster; astrona records it beforehand and restores it afterwards (or leaves it unset, if it was unset). A lab never silently re-points your plain `kubectl` at a different cluster.
- still lets kind add the `kind-<lab>` context to your own kubeconfig, so `kubectl --context kind-astro-<lab> …` works from any terminal.
- runs lab scripts and `command` validation checks with `KUBECONFIG=<lab kubeconfig>`.
- deletes the file on `astrona destroy`.

To work in a lab:

```sh
astrona shell my-lab                      # your $SHELL with KUBECONFIG set; `exit` to leave
astrona shell my-lab -- k9s               # or run one command
export KUBECONFIG=$(astrona kubeconfig my-lab)
```

Inside `astrona shell`, `$ASTRONA_LAB` holds the lab name (e.g. add it to your prompt). With no lab-name, both commands use the lab config from `-c`, or the only running kind lab. A lab created by an older astrona (no isolated kubeconfig yet) gets one generated on first use.

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

`astrona run` starts them last, after bootstrap scripts, manifests and `waitFor` gates, waits up to 30s for each to become ready, and prints how to reach them:

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
