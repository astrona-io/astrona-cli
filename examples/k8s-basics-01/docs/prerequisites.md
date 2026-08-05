# Prerequisites: k8s-basics-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.prerequisites` — read this before attempting the [exam question](./exam-question.md).

## Knowledge

You should already be comfortable with:

- **`kubectl` basics** — `get`, `describe`, `create`, `apply -f`, and reading `-o yaml`/`-o jsonpath` output.
- **Namespaces** — what they are, why they scope resources, and how to target one with `-n`/`--namespace`.
- **ConfigMaps** — what they're for (non-secret config data) and the two common ways to create one: imperatively (`kubectl create configmap`) or declaratively (a YAML manifest applied with `kubectl apply -f`).
- **`kubectl` contexts** — a kubeconfig can hold multiple clusters; `--context <name>` (or `kubectl config use-context`) picks which one a command runs against. This matters here because Astrona always targets `kind-<cluster-name>` explicitly rather than relying on whatever your current context happens to be.
- **Basic shell usage** — this lab's bootstrap stage runs a `.sh` script; you don't need to write shell, just be unsurprised by it.

If any of the above is new, this maps to the CNCF CKAD "Application Environment, Configuration and Security" domain — any CKAD study guide's ConfigMap/Namespace sections will get you there.

## Tooling

Installed and on your `PATH`:

- [`kind`](https://kind.sigs.k8s.io/) — this lab runs on a local kind cluster, not a real one.
- [`kubectl`](https://kubernetes.io/docs/tasks/tools/#kubectl)
- Docker or Podman, running — Astrona auto-detects whichever is available.
- The `astrona` CLI itself (`astrona run`/`submit`/`destroy` — see the repo [README](../../../README.md) if these are unfamiliar). `submit` hands your work to the Proctor for grading; you don't grade yourself.

## Not required

- Writing Kubernetes Operators, Helm charts, or anything beyond core `kubectl`.
- Prior familiarity with Astrona's own config format (`config.yaml`) — you don't need to read or touch it to complete this lab.

Ready? Go to the [exam question](./exam-question.md).
