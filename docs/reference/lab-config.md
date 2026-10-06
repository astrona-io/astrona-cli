# Lab Config Schema

The full shape of `config.yaml` (`internal/config.LabConfig`). Every field is optional unless noted — an empty/omitted block simply means that stage does nothing.

## Editor support and validation

Add this first line to a lab's `config.yaml` for autocompletion, hover docs and inline errors in VS Code (with the Red Hat *YAML* extension) and any other editor using yaml-language-server:

```yaml
# yaml-language-server: $schema=https://cli.astrona.io/schema/lab-config.schema.json
```

The [JSON Schema](https://cli.astrona.io/schema/lab-config.schema.json) is generated from astrona's own config types, so it always matches what the CLI accepts — every object rejects unknown keys, and closed value sets (`runtime.type`, `type: file|folder|url`, check types, …) are enums. It expects the documented spelling (`podReady`); the CLI itself also accepts other letter case for check and source types.

From the command line:

```sh
astrona validate -c ./labs/my-lab
```

checks the config without creating anything and exits non-zero on any problem — good as a CI step for lab repositories:

```text
  ✗  line 3                                 unknown field "waitfor" in bootstrap/testing
        fix: did you mean "waitFor"?
  ✗  validation.checks[1]                   unsupported type 'httpGet' (resourceExists, podReady or command)
  ✗  teardown.init[0] cleanup               local file source does not exist: …/cleanup.sh
```

**Unknown fields** (typos like `waitfor` or `manifest`) used to be silently ignored, leaving a lab without the gates or manifests its author meant to configure. Now `astrona validate` and `astrona check` fail on them, and `run`/`test`/`submit` print a warning with a did-you-mean (they still run, so a lab that has always worked keeps working).

## Top level

```yaml
metadata: {}     # MetadataConfig
runtime: {}      # RuntimeConfig — omit entirely for a kind cluster
bootstrap: {}    # BootstrapConfig
testing: {}      # BootstrapConfig (same shape, CI-only)
validation: {}   # ValidationConfig
teardown: {}     # TeardownConfig
```

## `metadata`

| Field | Type | Description |
|---|---|---|
| `name` | string | Lab name — becomes the cluster/VM name, prefixed `astro-` |
| `docs.prerequisites` | string | Path to a prerequisites doc |
| `docs.examQuestion` | string | Path to the formal, self-contained task statement |
| `docs.caseStudy` | string | Path to a softer, hint-driven version of the same task |
| `docs.guide` | string | Path to the full step-by-step walkthrough |

Doc paths are relative to `config.yaml` and must stay inside the lab directory; students read them with [`astrona docs`](cli/astrona_docs.md).

## `runtime`

| Field | Type | Description |
|---|---|---|
| `type` | string | `""`/`"kind"` (default) or `"qemu"` |
| `kind` | [Kind cluster](#runtimekind) | kind only — node image, node count, networking. Omit for kind's defaults |
| `qemu` | list of [QEMU VM](#runtimeqemun) | Required when `type: qemu`; always a list, even for one VM |
| `networks` | list | `{name, cidr}` — named virtual network segments VMs can join |
| `portForwards` | list of [PortForward](#runtimeportforwardsn) | kind only — host-side port forwards started by `astrona run` |

### `runtime.portForwards[N]`

kind only (rejected for `type: qemu`). Each entry becomes a background, auto-restarting `kubectl port-forward` on `127.0.0.1`, started once `bootstrap` (scripts, manifests and `waitFor` gates) has finished. See [Port forwards](../concepts/runtimes.md#port-forwards).

| Field | Type | Description |
|---|---|---|
| `name` | string | Required. Unique, lowercase DNS label (`a-z`, `0-9`, `-`) — shown in `astrona port-forward list` |
| `resource` | string | Required. `<kind>/<name>`, kind one of `svc`/`service`, `pod`/`po`, `deploy`/`deployment`, `sts`/`statefulset`, `rs`/`replicaset` — e.g. `svc/frontend` |
| `namespace` | string | Namespace of `resource` (default `default`) |
| `hostPort` | int | Required. Local port, `1024`–`65535`, unique within the lab. Always bound to `127.0.0.1` |
| `targetPort` | int | Required. Port on the service/pod, `1`–`65535` |
| `scheme` | string | `http` \| `https` \| `tcp` (default) — only changes the URL printed after `run` |
| `description` | string | Optional, printed next to the URL after `run` |
| `cluster` | string | Forward from a [linked cluster](#runtimekindlabsn) (its name) instead of the lab's own — e.g. an identity provider's login page |

```yaml
runtime:
  type: kind
  portForwards:
    - name: web
      resource: svc/frontend
      hostPort: 8080
      targetPort: 80
      scheme: http
      description: Frontend UI
    - name: db
      resource: svc/postgres
      namespace: data
      hostPort: 5432
      targetPort: 5432
```

### `runtime.qemu[N]`

| Field | Type | Description |
|---|---|---|
| `name` | string | Required once there's more than one entry; leave empty for a single-VM lab |
| `image.type` | string | `"file"` \| `"url"` \| `"oci"` |
| `image.source` | string | Path, URL, or OCI reference, depending on `type` |
| `image.checksum` | string | e.g. `sha256:...` — recommended |
| `image.checksums` | map | Alternative: per-algorithm checksum map |
| `arch` | string | Guest architecture |
| `cpus` | int | vCPU count |
| `memoryMB` | int | Memory in MB |
| `diskSizeGB` | int | Overlay disk size in GB |
| `extraDisks` | list | `{sizeGB, format, serial}` — additional blank disks |
| `networks` | list | `{name, ipv4}` — attaches a NIC to a `runtime.networks` segment |
| `sshAccess` | list of strings | Other VM names this VM can SSH into passwordlessly (multi-VM only) |
| `sshPort` | int | Host-side forwarded SSH port |
| `sshPasswordAuth` | bool | Enable password auth in addition to key auth, for the `student` account only — the `astrona` automation account (see [Runtimes](../concepts/runtimes.md)) is always key-auth only |
| `display` | bool | Show a QEMU display window |
| `bootstrap` | BootstrapConfig | Runs only against this VM, after the shared root `bootstrap` (multi-VM only) |
| `validation` | ValidationConfig | Runs only against this VM, after the shared root `validation` (multi-VM only) |

See [Runtimes](../concepts/runtimes.md) for single- vs multi-VM behavior.

#### Disk device naming

Disks are attached in a fixed order, so the guest device names are stable:

| Guest device | Disk |
|---|---|
| `/dev/vda` | Main overlay disk (root filesystem, `diskSizeGB`). Always the boot disk. |
| `/dev/vdb` | `extraDisks[0]` |
| `/dev/vdc` | `extraDisks[1]` |
| ... | ... |

The cloud-init seed disk is also attached but carries no useful mount for a
lab. `/dev/vda` is guaranteed to be the boot disk regardless of how many
`extraDisks` entries are present.

For labs that must not hard-code a `/dev/vd*` name, set a `serial` on the
`extraDisks` entry and reference it by the stable
`/dev/disk/by-id/virtio-<serial>` path instead.

### `runtime.kind`

kind only (rejected for `type: qemu`). Omit it and the lab gets a plain `kind create cluster`: one control-plane node, kind's default node image, kindnet CNI. Every field is optional.

| Field | Type | Description |
|---|---|---|
| `version` | string | Kubernetes version, e.g. `v1.31.2` — boots `kindest/node:<version>`. Mutually exclusive with `image` |
| `image` | string | Full node image reference. Pin by digest for reproducible labs: `kindest/node:v1.31.2@sha256:…` |
| `nodes.controlPlanes` | int | `1`–`3` (default `1`). More than one adds kind's load balancer container |
| `nodes.workers` | int | `0`–`6` (default `0`) |
| `networking.disableDefaultCNI` | bool | Don't install kindnet — the lab installs its own CNI (Calico, Cilium, …) in `bootstrap`. Nodes stay `NotReady` until it does |
| `networking.kubeProxyMode` | string | `iptables` \| `ipvs` \| `nftables` \| `none` |
| `networking.ipFamily` | string | `ipv4` \| `ipv6` \| `dual` |
| `networking.podSubnet` | string | Pod CIDR, e.g. `192.168.0.0/16` (Calico's default) |
| `networking.serviceSubnet` | string | Service CIDR; must not overlap `podSubnet` |
| `featureGates` | map[string]bool | Kubernetes feature gates, e.g. `InPlacePodVerticalScaling: true` |
| `runtimeConfig` | map[string]string | API groups to enable/disable (`"true"`/`"false"`), e.g. `"resource.k8s.io/v1beta1": "true"` |
| `addons` | [Addons](#runtimekindaddons) | Cluster components installed right after the cluster is created |
| `preloadImages` | list of strings | Images loaded into every node before addons/bootstrap, e.g. `[nginx:1.27-alpine]` — explicit tag or digest required (not `:latest`), max 30. See [Preloaded images](../concepts/runtimes.md#preloaded-images-runtimekindpreloadimages) |
| `labs` | list of [KindLab](#runtimekindlabsn) | Extra kind clusters running side by side with the lab's own (max 5) |

```yaml
runtime:
  type: kind
  kind:
    version: v1.31.2
    nodes:
      workers: 2
    networking:
      disableDefaultCNI: true
      podSubnet: 192.168.0.0/16
```

#### `runtime.kind.addons`

Installed in this order, right after the cluster is created and before `bootstrap`; each one is waited on until ready. Each is pinned to one upstream release whose manifest checksum is built into astrona — see [Addons](../concepts/runtimes.md#addons-runtimekindaddons).

| Field | Type | Description |
|---|---|---|
| `cni` | string | `calico` (Calico v3.32.2). Implies `networking.disableDefaultCNI: true` and pod subnet `192.168.0.0/16` (set `podSubnet` to that or leave it out). Enforces NetworkPolicy |
| `certManager` | bool | cert-manager v1.21.2 |
| `metricsServer` | bool | metrics-server v0.9.0 (`kubectl top`, HPA), with `--kubelet-insecure-tls` for kind's self-signed kubelet certs |
| `gatewayAPI` | string | `envoy` — Gateway API CRDs + Envoy Gateway v1.9.2, with GatewayClass `eg` |
| `gatewayPorts.http` | int | Host port for Gateway listeners on port 80 (default `8080`, on `127.0.0.1`) |
| `gatewayPorts.https` | int | Host port for Gateway listeners on port 443 (default `8443`, on `127.0.0.1`) |

```yaml
runtime:
  kind:
    nodes: { workers: 1 }
    addons:
      cni: calico
      certManager: true
      metricsServer: true
      gatewayAPI: envoy        # Gateways with gatewayClassName: eg
```

#### `runtime.kind.labs[N]`

Extra kind clusters that run side by side with the lab's own — e.g. an identity provider, a database, a second "site". They belong to the lab: `astrona run` creates them first, and `stop`, `start`, `reset`, `destroy` and `test` handle them together with the lab. See the [Linked Labs guide](../guides/linked-labs.md).

| Field | Type | Description |
|---|---|---|
| `name` | string | Required. How the lab refers to the cluster: `ASTRONA_LINK_<NAME>_*` env vars, `astrona-links` ConfigMap keys, `cluster:` on checks. Lowercase letters, digits, `-`; max 20 characters; unique. The kind cluster is `astro-<lab>-<name>` |
| `version`, `image`, `nodes`, `networking`, `featureGates`, `runtimeConfig`, `addons`, `preloadImages` | | Same as for the lab's own cluster (above). A gateway addon gets no host ports here |
| `bootstrap` | [BootstrapConfig](#bootstrap-testing) | Sets this cluster up — runs before the lab's own bootstrap |
| `testing` | [BootstrapConfig](#bootstrap-testing) | This cluster's part of the reference solution — `astrona test` applies it before the lab's own `testing` |
| `dependsOn` | list of strings | Other linked clusters (names) that must be up and ready — bootstrap and `waitFor` done — before this one is created. Its scripts get their `ASTRONA_LINK_*` addresses (and its cluster their `astrona-links` ConfigMap). Unknown names, self-dependencies and cycles are rejected |

Paths are relative to the lab's config, like everywhere else; by convention each cluster keeps its files in `labs/<name>/` (`labs/idp/bootstrap/`, `labs/idp/testing/`).

Clusters are created one at a time: dependencies first, otherwise in the order listed; the lab's own cluster last. The first one that fails stops the run — nothing depending on it is started.

```yaml
runtime:
  type: kind
  kind:
    labs:
      - name: idp
        preloadImages: [nginx:1.27-alpine]
        bootstrap:
          manifests:
            - {name: idp, type: file, source: labs/idp/bootstrap/idp.yaml}
          waitFor:
            - {resource: deploy/idp, namespace: auth}
```

The node image must exist for the `kind` version installed on the student's machine — each [kind release](https://github.com/kubernetes-sigs/kind/releases) lists the node images built for it. Kind config features that reach outside the cluster (`extraMounts`, `extraPortMappings`, `kubeadmConfigPatches`) are deliberately not exposed — see [Runtimes](../concepts/runtimes.md#cluster-shape-runtimekind).

## `bootstrap` / `testing`

Both use the same shape (`BootstrapConfig`) — `testing` only runs under `astrona test`.

| Field | Type | Description |
|---|---|---|
| `init` | list of [ResourceItem](#resourceitem) | Scripts run in order at start |
| `manifests` | list of [ResourceItem](#resourceitem) | Applied via `kubectl apply` (requires a kubectl-reachable cluster) |
| `waitFor` | list of [WaitFor](#waitfor) | Readiness gates checked in order after `manifests` (kind only) |

### `WaitFor`

One readiness gate. Target exactly one of: a named object (`resource: deploy/web`), every object matching a label selector (`resource: pod` + `selector`), or every object of a kind (`resource: nodes` + `all: true`).

| Field | Type | Description |
|---|---|---|
| `name` | string | Optional label shown while waiting (defaults to the target) |
| `resource` | string | Required. `<kind>/<name>`, or a bare `<kind>` with `selector`/`all`. CRD kinds work too: `certificates.cert-manager.io/web-tls` |
| `selector` | string | Label selector, e.g. `app=web` or `tier in (fe,be)` |
| `all` | bool | Every object of `resource`'s kind |
| `namespace` | string | Default `default` (ignored for cluster-scoped kinds like nodes) |
| `condition` | string | `rollout` (`kubectl rollout status`) or a status condition for `kubectl wait --for=condition=<X>` (`Ready`, `Available`, `Complete`, `Established`, …) |
| `timeout` | string | Go duration, `1s`–`30m` (default `2m`) |

Default `condition` by kind — any other kind must set one:

| Kind | Default condition |
|---|---|
| `deploy`/`deployment`, `sts`/`statefulset`, `ds`/`daemonset` | `rollout` |
| `pod`, `node` | `Ready` |
| `job` | `Complete` |
| `crd`/`customresourcedefinition` | `Established` |

```yaml
bootstrap:
  waitFor:
    - resource: nodes
      all: true                 # e.g. after bootstrap installed a CNI
      timeout: 3m
    - resource: deploy/web
      namespace: shop
    - name: database
      resource: pod
      selector: app=postgres
      namespace: shop
      timeout: 5m
```

While a gate's target doesn't exist yet (`NotFound`, no matching resources, kind not registered yet) it's retried every 2s until the timeout; any other kubectl failure fails the gate immediately. `rollout` only works for deployments, statefulsets and daemonsets, and needs a name or `selector`.

## `validation`

| Field | Type | Description |
|---|---|---|
| `checks` | list of [ValidationCheck](#validationcheck) | Declarative checks |
| `script` | [ResourceItem](#resourceitem) | Single custom pass/fail script |
| `scripts` | list of [ResourceItem](#resourceitem) | Additional scripts, run after `script`, in order |
| `passPercent` | int | `1`–`100`: pass once the score reaches this percentage (e.g. `66`). Unset: every check and script must pass |

Validation scripts also take `hint` and `points` (see `ValidationCheck`).

### `ValidationCheck`

| Field | Type | Description |
|---|---|---|
| `name` | string | Label shown in the Proctor's report |
| `type` | string | `"resourceExists"` \| `"podReady"` \| `"command"` \| `"jsonpath"` \| `"count"` \| `"http"` — see [Grading](../concepts/grading.md) |
| `resource` | string | `kubectl get` arguments (for `resourceExists`/`podReady`/`jsonpath`/`count`), e.g. `deploy/web -n shop` or `pods -l app=web -n shop` |
| `command` | string | Shell words to run directly, no shell interpolation (for `command`) |
| `expect` | string | Exact expected value (trimmed stdout / JSONPath value / body) |
| `contains` | string | Value must contain this |
| `expectRegex` | string | Value must match this regular expression (Go syntax) |
| `jsonpath` | string | `jsonpath` checks: kubectl JSONPath, e.g. `{.spec.replicas}` |
| `min` / `max` | int | `count` checks: bounds on the number of objects (at least one required) |
| `url` | string | `http` checks: `http://` or `https://` URL |
| `expectStatus` | int | `http` checks: expected status (default `200`) |
| `hint` | string | Shown to the student only when this check fails — a nudge, not the answer |
| `points` | int | Weight in the score (default `1`) |
| `cluster` | string | Grade the check in this linked cluster (a [`runtime.kind.labs`](#runtimekindlabsn) name) instead of the lab's own. Not for `http` checks |

## `exam`

Turns the lab into a timed exam. Omit it for normal practice labs.

| Field | Type | Description |
|---|---|---|
| `timeLimit` | string | Go duration, `1m`–`24h`, e.g. `2h`. The clock starts when `astrona run` (or `reset`) has the lab ready |
| `hideHints` | bool | `astrona submit` never shows check hints |
| `strict` | bool | A submission after the time limit can't pass |

See [Grading → Exam mode](../concepts/grading.md#exam-mode).

## `teardown`

| Field | Type | Description |
|---|---|---|
| `init` | list of [ResourceItem](#resourceitem) | Best-effort scripts run before the environment is destroyed |
| `keepCluster` | bool | Skip destroying the environment after teardown scripts run |

## `ResourceItem`

Shared shape for every script/manifest reference (`bootstrap.init`, `bootstrap.manifests`, `testing.*`, `teardown.init`, `validation.script`, `validation.scripts`).

| Field | Type | Description |
|---|---|---|
| `name` | string | Label printed as the step runs |
| `description` | string | Optional, printed alongside `name` |
| `type` | string | `"file"` \| `"folder"` \| `"url"` (manifests support all three; scripts too) |
| `source` | string | Path (relative to the lab's base directory) or URL, depending on `type` |
| `hint` | string | Validation scripts only: shown when the script fails |
| `points` | int | Validation scripts only: weight in the score (default `1`) |

## Full example

```yaml
metadata:
  name: "k8s-basics-01"
  docs:
    prerequisites: "docs/prerequisites.md"
    examQuestion: "docs/exam-question.md"
    caseStudy: "docs/case-study.md"
    guide: "docs/step-by-step-guide.md"

bootstrap:
  init:
    - name: "echo"
      type: "file"
      source: "hello.sh"
  manifests: []

testing:
  manifests:
    - name: "solution"
      type: "folder"
      source: "solution"

validation:
  checks:
    - name: "lab-ns namespace exists"
      type: "resourceExists"
      resource: "namespace/lab-ns"
  script:
    type: "file"
    source: "validate.sh"
  scripts:
    - name: "check-network"
      type: "file"
      source: "validate-network.sh"

teardown:
  init:
    - name: "dump-logs"
      type: "file"
      source: "teardown/dump-logs.sh"
  keepCluster: false
```

For the CLI's own flags and commands, see the [CLI Reference](cli/astrona.md).
