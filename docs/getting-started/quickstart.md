# Quickstart

This walks through the full loop — bring a lab up, work on it, get graded, tear it down — using the [`examples/k8s-basics-01`](https://github.com/astrona-io/astrona-cli/tree/main/examples/k8s-basics-01) lab shipped in the repo. Clone the repo first, or point `--git` at it directly (see [Remote Config Sources](../guides/remote-config.md)).

## 1. Check your toolchain

```sh
astrona check
```

Fix anything reported as `✗` before continuing — `kind`, Docker/Podman, and `kubectl` are required for this example.

## 2. Bring the lab up

```sh
astrona run -c examples/k8s-basics-01
```

This:

1. Reads `examples/k8s-basics-01/config.yaml`.
2. Creates a `kind` cluster named `astro-k8s-basics-01` (astrona prefixes every cluster it creates with `astro-`).
3. Runs the lab's `bootstrap.init` scripts (here, `hello.sh`).
4. Applies any `bootstrap.manifests`.

At this point the cluster is up and the trainee's actual work begins — this particular lab expects a `lab-ns` namespace with a `hello-config` ConfigMap holding specific content, which isn't created for you (that's the exercise).

## 3. Inspect it

```sh
astrona list
```

Shows every astrona-managed lab currently running (kind clusters and qemu VMs), with runtime, status, and uptime — a `kubectl get`-style table.

```sh
kubectl --context kind-astro-k8s-basics-01 get ns
```

## 4. Submit for grading

```sh
astrona submit -c examples/k8s-basics-01
```

Hands the running cluster to the [Proctor](../concepts/grading.md), which runs this lab's `validation.checks` (does `lab-ns` exist? does `hello-config` exist?) and its `validation.script` (does the ConfigMap actually hold the right content?). Output is pytest-style — a PASS/FAIL line per check, then a summary — and the command's exit code reflects the verdict, so it's safe to gate a script on.

## 5. Tear it down

```sh
astrona destroy -c examples/k8s-basics-01
```

Runs `teardown.init` scripts, then deletes the cluster (unless the config sets `teardown.keepCluster: true`).

## The lab-author loop

If you're *authoring* a lab rather than taking one, `astrona test` runs the whole thing non-interactively against a reference solution, to prove the lab is actually solvable before you publish it:

```sh
astrona test -c examples/k8s-basics-01 --junit-xml=report.xml
```

This bootstraps, applies `testing.manifests` (the reference solution — here, `examples/k8s-basics-01/solution/`), submits to the Proctor, and always tears down afterwards, even on failure. It runs against a `test-`-prefixed cluster name so it never collides with a lab you already have up via `astrona run`. See [CI Integration](../guides/ci-integration.md) for wiring this into a pipeline.

## Next

- [Authoring a Lab](../guides/authoring-a-lab.md) — build your own `config.yaml` from scratch.
- [Runtimes](../concepts/runtimes.md) — when to reach for `qemu` instead of the default `kind` backend.
