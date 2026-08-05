# Step-by-Step Guide: k8s-basics-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.guide` — that field is the canonical pointer to this file.

This walks through the full solution to the [case study](./case-study.md) / [exam question](./exam-question.md). Read those first if you want to try it yourself before seeing the answer.

## 1. What this lab expects

Open [`../config.yaml`](../config.yaml) and look at four sections:

- `bootstrap.init` — runs `hello.sh` when the cluster comes up, just to prove bootstrap worked.
- `validation.checks` — declarative grading: a `namespace/lab-ns` and a `configmap/hello-config -n lab-ns` must both exist.
- `validation.script` — points at `validate.sh`, a custom check that goes further than existence: it reads `hello-config`'s `data.message` and fails unless it's exactly `hello world`.
- `testing.manifests` — points at `../solution/`, the reference answer CI applies automatically via `astrona test`. You're about to do by hand what that folder does automatically.
- `teardown.init` — runs `teardown/dump-logs.sh` before the cluster is deleted.

## 2. Start the lab

From inside `examples/k8s-basics-01/`:

```sh
astrona run -c .
```

This creates the kind cluster (via Docker or Podman, auto-detected) and runs `bootstrap.init`, so you should see `Hello world` printed.

## 3. Satisfy the checks

`validation.checks` needs a namespace and a configmap, and `validation.script` needs that configmap's `message` data to be exactly `hello world`. Either apply the reference manifest directly:

```sh
kubectl --context kind-k8s-basics-01 apply -f solution/
```

or do it imperatively, which is exactly what that manifest declares:

```sh
kubectl --context kind-k8s-basics-01 create namespace lab-ns
kubectl --context kind-k8s-basics-01 create configmap hello-config -n lab-ns --from-literal=message="hello world"
```

(`kind-k8s-basics-01` is `kind-` + `metadata.name` from `config.yaml` — that's the context Astrona always targets, regardless of what your current `kubectl` context happens to be.)

## 4. Submit to the Proctor

You don't grade yourself — you submit, and the Proctor grades:

```sh
astrona submit -c .
```

Expected output:

```
Submitting to the Proctor...
  PASS  lab-ns namespace exists (0.03s)
  PASS  hello-config configmap exists (0.03s)
  PASS  validation script (0.03s)

3 passed, 0 failed in 0.09s

PROCTOR: PASS
```

Exit code is `0` on pass, `1` on fail — if you're scripting this, check `$?` rather than parsing the text. For CI systems that want native test-result reporting instead of parsing stdout, add `--junit-xml=report.xml` to write a JUnit XML report GitHub Actions/GitLab CI/Jenkins can render directly.

## 5. Tear down

```sh
astrona destroy -c .
```

Runs `teardown.init` (`dump-logs.sh`) first, then deletes the kind cluster.

## The automated version

Everything above — bootstrap, applying the solution, submitting to the Proctor, tearing down — is exactly what `astrona test -c .` does in one command, using `testing.manifests` (`../solution/`) instead of your manual `kubectl` calls. That's what lab authors run in CI to prove the reference solution actually passes the Proctor's own checks before publishing it. It tears down even if grading fails, so it never leaks a cluster.

Note `astrona test` runs against `kind-test-k8s-basics-01`, not `kind-k8s-basics-01` — the `test-` prefix keeps a CI run from colliding with a cluster you already have up via `astrona run`.
