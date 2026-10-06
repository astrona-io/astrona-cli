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

Clusters are created **one at a time**, never in parallel: every linked cluster first, then the lab's own. Each one is only created once the previous is fully ready — its bootstrap done and its `waitFor` gates passed. Use `dependsOn` when one linked cluster needs another:

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
```

## Reaching a linked cluster

All kind clusters share one container network, so pods and host scripts can reach a linked cluster's **node** by its container name — `<cluster>-control-plane`. Publish what the lab should reach as a **NodePort** Service with a fixed `nodePort`:

```yaml
apiVersion: v1
kind: Service
metadata: {name: idp, namespace: auth}
spec:
  type: NodePort
  selector: {app: idp}
  ports: [{port: 80, targetPort: 80, nodePort: 30080}]
```

From the lab's cluster: `http://<host>:30080/`. A `ClusterIP` Service name or a pod IP from the other cluster is **not** reachable — each cluster has its own service and pod network.

Astrona tells the lab where each linked cluster is:

| Where | What |
|---|---|
| The lab's bootstrap/testing/teardown scripts and `command` checks | `ASTRONA_LINK_<NAME>_HOST`, `ASTRONA_LINK_<NAME>_CONTEXT`, `ASTRONA_LINK_<NAME>_KUBECONFIG` (`<NAME>` upper-cased, `-` → `_`) |
| In the lab's cluster | ConfigMap `astrona-links` in namespace `default`: `<name>.host`, `<name>.context` |
| `astrona shell` | Every cluster's kubeconfig: `kubectl --context "$ASTRONA_LINK_IDP_CONTEXT" …` works, plus the env vars. `astrona shell <lab> --cluster idp` makes the idp cluster the default context; `astrona kubeconfig <lab> --cluster idp` prints its kubeconfig |

A linked cluster's own `bootstrap`/`testing` scripts run with `KUBECONFIG` pointing at that cluster.

**Never hard-code a linked cluster's host or name.** Under `astrona test` every cluster has a different name (`astro-test-…`); read it from the env var or the ConfigMap instead.

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
      hostPort: 18080
      scheme: http
```

It belongs to the lab like any forward: started after every cluster is up, paused and resumed by `stop`/`start`, removed by `destroy`, listed by `astrona port-forward list`. Like all forwards it binds `127.0.0.1` only.

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
| `astrona reset` / `destroy` | Removes them with the lab |
| `astrona test` | Creates test copies of every cluster, applies each linked cluster's `testing`, then the lab's, grades, and tears everything down — even on failure |
| `astrona check` | Counts their memory in the lab's estimate |
| `astrona diagnose` / `test --diagnostics` | Collects every linked cluster too, under `clusters/<cluster>/` in the bundle |

Before bootstrap scripts run, `astrona run` and `astrona test` wait for the cluster's DNS (CoreDNS) — a script resolving a linked cluster's host in the first seconds after a cluster is created would otherwise fail with `bad address`.

`astrona bundle` doesn't support labs with linked clusters yet.

## Planning resources

Each linked cluster is a full kind cluster: budget memory for all of them together (roughly 1–1.5 GiB per single-node cluster before workloads) — `astrona check -c <lab>` estimates it. A linked cluster's gateway addon gets no host ports, so it never clashes with the lab's own.
