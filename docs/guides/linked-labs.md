# Linked Labs

Some things can only be practised with two systems: an app logging in against an external identity provider, a client calling a service in another cluster, network policy between "sites". A lab can run **extra kind clusters side by side** with its own — declared under `runtime.kind.labs` in the same `config.yaml` — and knows how to reach them.

The worked reference is [`examples/linked-labs-01`](https://github.com/astrona-io/astrona-cli/tree/main/examples/linked-labs-01): an `idp` cluster serving a token endpoint, and a task to log in against it from the lab's own cluster. `astrona init lab <dir> --linked` scaffolds a lab like it.

## Declaring linked clusters

```yaml
metadata:
  name: auth-lab

runtime:
  type: kind
  kind:
    preloadImages: [curlimages/curl:8.11.1]     # the lab's own cluster
    labs:
      - name: idp                                # kind cluster astro-auth-lab-idp
        preloadImages: [nginx:1.27-alpine]
        nodes: {workers: 0}
        addons: {certManager: true}
        bootstrap:
          manifests:
            - {name: idp, type: file, source: labs/idp/bootstrap/idp.yaml}
          waitFor:
            - {resource: deploy/idp, namespace: auth}
        testing:                                 # its part of the reference solution
          init:
            - {name: seed users, type: file, source: labs/idp/testing/seed.sh}
```

Each entry takes the same cluster fields as `runtime.kind` (version, nodes, networking, addons, preloadImages, …) plus its own `bootstrap` and `testing`. Up to 5 per lab; names are lowercase letters, digits and `-` (max 20 characters). See [`runtime.kind.labs`](../reference/lab-config.md#runtimekindlabsn).

### Dependencies and start order

By default clusters are created **one at a time**: every linked cluster first, then the lab's own. Each one is only created once the previous is fully ready — its bootstrap done and its `waitFor` gates passed. Use `dependsOn` when one linked cluster needs another:

```yaml
    labs:
      - name: db
        bootstrap: {manifests: [{name: db, type: file, source: labs/db/bootstrap/db.yaml}],
                    waitFor: [{resource: statefulset/db, namespace: data}]}
      - name: idp
        dependsOn: [db]          # created only once db is ready
        bootstrap:
          init:
            - {name: configure, type: file, source: labs/idp/bootstrap/configure.sh}   # gets $ASTRONA_LINK_DB_HOST
```

- Dependencies are created first; otherwise clusters start in the order listed.
- A cluster's scripts get the `ASTRONA_LINK_*` addresses of the clusters it depends on (the lab's own scripts get all of them).
- If a cluster fails, the run stops there: nothing that depends on it — and not the lab itself — is started, and the error says what was skipped. `astrona destroy` cleans up what was created.
- Unknown names, a cluster depending on itself, and cycles are rejected by `astrona validate`.
- `astrona test` applies the clusters' `testing` blocks in the same order; `astrona start` restarts them in it.

#### Creating clusters in parallel

`--parallel N` (on `run`, `reset` and `test`, max 5) creates up to N linked clusters at once — faster on a machine with memory to spare:

```sh
astrona run -c ./my-lab --parallel 3
```

- `dependsOn` still holds: a cluster starts as soon as everything it depends on is ready, so independent clusters come up together.
- After the first failure **nothing new starts**; clusters already being created finish, and the error lists what was never started. `astrona destroy` cleans up what exists.
- Each cluster logs to its own run log (`~/.astrona/logs/linked-<cluster>-…`); the screen shows one line per cluster as it becomes ready or fails, with the log path on failure.
- The lab's own cluster is still created last, after all of them.

### Folder layout

Paths are relative to `config.yaml`, like everywhere else. By convention each linked cluster keeps its files in its own folder under `labs/`, next to `docs/` and `solution/`:

```
config.yaml
docs/                     # student docs
solution/                 # the lab's own reference solution
labs/
  idp/
    bootstrap/            # sets up the idp cluster
    testing/              # the idp cluster's part of the reference solution
    teardown/             # its teardown scripts
```

## Reaching a linked cluster

All kind clusters share one container network, so a pod can reach a linked cluster's **node**. Publish what the lab should reach as a **NodePort** Service with a fixed `nodePort`:

```yaml
apiVersion: v1
kind: Service
metadata: {name: idp, namespace: auth}
spec:
  type: NodePort
  selector: {app: idp}
  ports: [{port: 80, targetPort: 80, nodePort: 30080}]
```

From any cluster of the lab: **`http://idp.astrona.internal:30080/`**. A `ClusterIP` Service name or a pod IP from the other cluster is **not** reachable — each cluster has its own service and pod network.

### Stable names: `<name>.astrona.internal`

Every linked cluster gets a fixed DNS name, `<name>.astrona.internal`, that resolves to its node in every cluster of the lab — the lab's own and the other linked clusters. It's the **same name under `astrona run` and `astrona test`** (where the container names differ), so manifests and configs can simply contain it: an OIDC issuer URL, a database host, a redirect URI.

- Astrona looks up each node's IPv4 and writes it into each cluster's CoreDNS config (a marked block in the `coredns` ConfigMap), refreshing it after `start` and `reset --cluster`, when IPs can change.
- `.internal` is reserved for private use. (`*.localhost` wouldn't work: curl and other clients resolve it to `127.0.0.1` without asking DNS.)
- A lab that replaces CoreDNS's config with one lacking a `.:53` server block keeps the container names (`$ASTRONA_LINK_<NAME>_HOST`) instead.

**The same URL on your machine:** forward the service with `hostPort` equal to its `nodePort` (see [below](#reaching-a-linked-cluster-from-your-machine)) and add the name to `/etc/hosts` once — then `http://idp.astrona.internal:30080` works in your browser and in the cluster, which is what an identity provider's issuer URL needs:

```
127.0.0.1 idp.astrona.internal
```

(astrona never edits `/etc/hosts` itself.)

### Everything astrona publishes

Astrona tells the lab where each linked cluster is:

| Where | What |
|---|---|
| Every cluster's DNS | `<name>.astrona.internal` |
| The lab's bootstrap/testing/teardown scripts and `command` checks | `ASTRONA_LINK_<NAME>_HOSTNAME` (the stable name), `_HOST` (the node's container name), `_CONTEXT`, `_KUBECONFIG` (`<NAME>` upper-cased, `-` → `_`) |
| In the lab's cluster | ConfigMap `astrona-links` in namespace `default`: `<name>.hostname`, `<name>.host`, `<name>.context` |
| `astrona shell` | Every cluster's kubeconfig: `kubectl --context "$ASTRONA_LINK_IDP_CONTEXT" …` works, plus the env vars. `astrona shell <lab> --cluster idp` makes the idp cluster the default context; `astrona kubeconfig <lab> --cluster idp` prints its kubeconfig |

A linked cluster's own `bootstrap`/`testing` scripts run with `KUBECONFIG` pointing at that cluster.

**Use the stable name, not the container name.** Under `astrona test` every cluster has a different container name (`astro-test-…`) — `<name>.astrona.internal` doesn't change.

## Reaching a linked cluster from your machine

A port forward reaches a linked cluster with `cluster:` — the student's browser can open the identity provider's login page, a database client can connect to the db cluster:

```yaml
runtime:
  portForwards:
    - name: idp
      cluster: idp              # forwarded from the idp cluster
      resource: svc/idp
      namespace: auth
      targetPort: 80
      hostPort: 30080           # = the NodePort, so one URL works everywhere
      scheme: http
```

It belongs to the lab like any forward: started after every cluster is up, paused and resumed by `stop`/`start`, removed by `destroy`, listed by `astrona port-forward list`. Like all forwards it binds `127.0.0.1` only.

## TLS between clusters: a shared CA

An identity provider is usually reached over HTTPS, and its clients must trust its certificate. `runtime.kind.sharedCA: true` gives the lab its **own certificate authority**, trusted by every cluster of the lab:

```yaml
runtime:
  type: kind
  kind:
    sharedCA: true
    labs:
      - name: idp
        addons: {certManager: true}
        bootstrap:
          manifests:
            - {name: idp, type: file, source: labs/idp/bootstrap/idp.yaml}
          waitFor:
            - {resource: certificate/idp, namespace: auth, condition: Ready, timeout: 3m}
```

What every cluster (the lab's own and each linked one) gets:

| | |
|---|---|
| ConfigMap `astrona-ca` in `default` | `ca.crt` — mount it in a pod to trust the CA (`curl --cacert /ca/ca.crt …`, `SSL_CERT_FILE`, a Java truststore, …) |
| Secret `astrona-ca` (`kubernetes.io/tls`) | The CA's certificate and key — in `cert-manager` when the cluster has the `certManager` addon, in `default` otherwise |
| ClusterIssuer `astrona-ca` | Only with the `certManager` addon — issue certificates from the lab CA with `issuerRef: {kind: ClusterIssuer, name: astrona-ca}` |

A certificate for the stable name then verifies from any cluster of the lab:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata: {name: idp, namespace: auth}
spec:
  secretName: idp-tls
  dnsNames: [idp.astrona.internal]
  issuerRef: {kind: ClusterIssuer, name: astrona-ca}
```

- The CA is created per lab run (ECDSA P-256, can only sign leaf certificates) at `~/.astrona/kind/<lab>/ca.crt` (key `ca.key`, mode `0600`) and deleted with the lab; `astrona test` copies get their own.
- Scripts, command checks and `astrona shell` get `$ASTRONA_CA_CERT` — the path of `ca.crt` — for `curl --cacert "$ASTRONA_CA_CERT" …` from your machine (with the [`/etc/hosts` line](#stable-names-nameastronainternal) and a port forward).
- astrona never adds the CA to your system's trust store.
- `sharedCA` is lab-wide: set it on `runtime.kind`, not on a linked cluster.

## Grading across clusters

A check grades the lab's own cluster unless it names a linked one with `cluster:`:

```yaml
validation:
  checks:
    - name: idp runs 2 replicas
      cluster: idp
      type: jsonpath
      resource: deploy/idp -n auth
      jsonpath: "{.status.readyReplicas}"
      expect: "2"
```

Works for every check type except `http` (which runs from your machine). A `command` check with `cluster:` gets that cluster's `KUBECONFIG`.

## Lifecycle

Linked clusters belong to the lab:

| Command | What happens |
|---|---|
| `astrona run` | Creates the linked clusters (bootstrap included), then the lab. Refused while the lab is running — `astrona reset` starts it over |
| `astrona list` / `status` | A linked cluster is listed as `linked cluster of <lab>`; `status` shows each one's state. Commands that pick "the only running lab" ignore linked clusters |
| `astrona stop` / `start` | Stops/starts them with the lab (`start` brings them up first) |
| `astrona reset --cluster idp` | Rebuilds only that linked cluster of the running lab: its teardown, destroy, then create + bootstrap again (with the addresses of what it dependsOn). The lab and its other clusters are left alone; port forwards into it are restarted. Same name, so the lab's `ASTRONA_LINK_*`/`astrona-links` stay valid |
| `astrona reset` / `destroy` | Runs the lab's teardown, then each linked cluster's `teardown.init` (reverse start order, `KUBECONFIG` pointing at it), then removes everything. `teardown.keepCluster` keeps them all |
| `astrona test` | Creates test copies of every cluster, applies each linked cluster's `testing`, then the lab's, grades, and tears everything down — even on failure |
| `astrona check` | Counts their memory in the lab's estimate |
| `astrona diagnose` / `test --diagnostics` | Collects every linked cluster too, under `clusters/<cluster>/` in the bundle |

Before bootstrap scripts run, `astrona run` and `astrona test` wait for the cluster's DNS (CoreDNS) — a script resolving a linked cluster's host in the first seconds after a cluster is created would otherwise fail with `bad address`.

`astrona bundle` packs linked clusters too — each must pin its node image (`version` or `image`); see [Offline Bundles](offline-bundles.md).

## Planning resources

Each linked cluster is a full kind cluster: budget memory for all of them together (roughly 1–1.5 GiB per single-node cluster before workloads) — `astrona check -c <lab>` estimates it. A linked cluster's gateway addon gets no host ports, so it never clashes with the lab's own.
