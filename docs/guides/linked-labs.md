# Linked Labs

Some things can only be practised with two systems: an app logging in against an external identity provider, a client calling a service in another cluster, network policy between "sites". A **linked lab** runs side by side with other labs — each in its own kind cluster — and knows how to reach them.

The worked reference is [`examples/linked-labs`](https://github.com/astrona-io/astrona-cli/tree/main/examples/linked-labs): an `idp` lab serving a token endpoint, and an `app` lab whose task is to log in against it.

## Declaring links

```yaml
metadata:
  name: linked-app

links:
  - name: idp          # how this lab refers to it: env vars, ConfigMap keys
    lab: ../idp        # a path to the other lab's directory (relative to this config)…
  - name: legacy
    lab: legacy-crm    # …or the name of a lab that's already running
```

- **Path** (contains `/`, or is `.`/`..`, or ends in `.yaml`/`.yml`): `astrona run` starts that lab first if it isn't running, with the same trust check as any lab. For a `--git` lab the path must stay inside the cloned repository.
- **Name**: the lab must already be running (`astrona run` it first). A lab loaded from a URL can only link by name — a remote config never starts a lab from elsewhere on your disk.

Up to 5 links per lab, nested at most 3 levels deep; a cycle is an error. Link names are lowercase letters, digits and `-` (max 20 characters). Linked labs must be kind labs.

## Reaching a linked lab

All kind clusters share one container network, so pods and host scripts can reach another lab's **node** by its container name — `<cluster>-control-plane`. Publish what the other lab should reach as a **NodePort** Service with a fixed `nodePort`:

```yaml
apiVersion: v1
kind: Service
metadata: {name: idp, namespace: auth}
spec:
  type: NodePort
  selector: {app: idp}
  ports: [{port: 80, targetPort: 80, nodePort: 30080}]
```

From the other cluster: `http://<host>:30080/`. A `ClusterIP` Service name or a pod IP from the other cluster is **not** reachable — each cluster has its own service and pod network.

Astrona tells the lab where each link is:

| Where | What |
|---|---|
| Bootstrap/testing/teardown scripts and `command` checks | `ASTRONA_LINK_<NAME>_HOST`, `ASTRONA_LINK_<NAME>_CONTEXT`, `ASTRONA_LINK_<NAME>_KUBECONFIG` (`<NAME>` upper-cased, `-` → `_`) |
| In the cluster | ConfigMap `astrona-links` in namespace `default`: `<name>.host`, `<name>.context` |
| `astrona shell` | Both labs' kubeconfigs: `kubectl --context kind-<other cluster> …` works, plus the env vars |

**Never hard-code the other lab's host or cluster name.** Under `astrona test` the linked lab is a test copy with a different name (`astro-test-…`); read it from the env var or the ConfigMap instead.

Before bootstrap scripts run, `astrona run` and `astrona test` wait for the cluster's DNS (CoreDNS) — a script resolving the linked lab's host in the first seconds after the cluster is created would otherwise fail with `bad address`.

## Running

```sh
astrona run -c examples/linked-labs/app    # starts idp (if needed), then app
astrona status linked-app                  # Links  idp → astro-linked-idp (Ready) · host …
astrona shell linked-app                   # both contexts available
astrona submit -c examples/linked-labs/app
```

Destroying a lab leaves its linked labs running (another lab may use them) and tells you which are still up — `astrona destroy <name>` each when you're done.

## Testing in CI

`astrona test` starts **test copies** of path-linked labs (`astro-test-<lab>`, no port forwards or gateway host ports, so they never clash with a real `run`), runs the lab's lifecycle against them, and tears every copy down afterwards — even on failure. A link by name uses that running lab as-is and never tears it down. Test copies don't start nested links yet.

## Planning resources

Each linked lab is a full kind cluster: budget memory for all of them together (roughly 1–1.5 GiB per single-node cluster before workloads), and give each lab a different `nodePort` range and different `runtime.portForwards` host ports if you run them side by side.
