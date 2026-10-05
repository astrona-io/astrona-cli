# Lab Config Schema

The full shape of `config.yaml` (`internal/config.LabConfig`). Every field is optional unless noted — an empty/omitted block simply means that stage does nothing.

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

## `runtime`

| Field | Type | Description |
|---|---|---|
| `type` | string | `""`/`"kind"` (default) or `"qemu"` |
| `kind` | [Kind cluster](#runtimekind) | kind only — node image, node count, networking. Omit for kind's defaults |
| `qemu` | list of [QEMU VM](#runtimeqemun) | Required when `type: qemu`; always a list, even for one VM |
| `networks` | list | `{name, cidr}` — named virtual network segments VMs can join |

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

The node image must exist for the `kind` version installed on the student's machine — each [kind release](https://github.com/kubernetes-sigs/kind/releases) lists the node images built for it. Kind config features that reach outside the cluster (`extraMounts`, `extraPortMappings`, `kubeadmConfigPatches`) are deliberately not exposed — see [Runtimes](../concepts/runtimes.md#cluster-shape-runtimekind).

## `bootstrap` / `testing`

Both use the same shape (`BootstrapConfig`) — `testing` only runs under `astrona test`.

| Field | Type | Description |
|---|---|---|
| `init` | list of [ResourceItem](#resourceitem) | Scripts run in order at start |
| `manifests` | list of [ResourceItem](#resourceitem) | Applied via `kubectl apply` (requires a kubectl-reachable cluster) |

## `validation`

| Field | Type | Description |
|---|---|---|
| `checks` | list of [ValidationCheck](#validationcheck) | Declarative checks |
| `script` | [ResourceItem](#resourceitem) | Single custom pass/fail script |
| `scripts` | list of [ResourceItem](#resourceitem) | Additional scripts, run after `script`, in order |

### `ValidationCheck`

| Field | Type | Description |
|---|---|---|
| `name` | string | Label shown in the Proctor's report |
| `type` | string | `"resourceExists"` \| `"podReady"` \| `"command"` |
| `resource` | string | `kubectl` resource selector (for `resourceExists`/`podReady`) |
| `command` | string | Shell words to run directly, no shell interpolation (for `command`) |
| `expect` | string | Exact expected trimmed stdout (for `command`; omit to just check exit code) |

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
