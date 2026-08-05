# Exam Question: k8s-basics-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.examQuestion`. Make sure you meet the [prerequisites](./prerequisites.md) first.

**Task weight: 100%** (this lab has a single scored task)

---

## Context

A kind-provisioned Kubernetes cluster has been prepared for you and is reachable via the `kubectl` context:

```
kind-k8s-basics-01
```

Use `--context kind-k8s-basics-01` on every `kubectl` command, or switch to it with `kubectl config use-context kind-k8s-basics-01`, so you don't accidentally target the wrong cluster.

## Task

Perform the following:

1. Create a `Namespace` named:
   ```
   lab-ns
   ```

2. Create a `ConfigMap` named:
   ```
   hello-config
   ```
   in the `lab-ns` namespace, with a data key `message` set to exactly:
   ```
   hello world
   ```

You may create these resources imperatively (`kubectl create ...`) or declaratively (write a manifest and `kubectl apply -f ...`) — either is acceptable.

## Constraints

- Do not modify `config.yaml`, or anything under `docs/` or `solution/` in this lab directory. Grading reads `config.yaml` as-is.
- Do not delete or recreate the cluster mid-task — start it once with `astrona run` and work against that cluster until you submit.

## How you will be graded

You do not grade yourself. From the `examples/k8s-basics-01/` directory, submit your work to the Proctor:

```sh
astrona submit -c .
```

The Proctor runs two kinds of checks: automated checks confirming the resources exist, and a script that inspects the `ConfigMap`'s actual content — so getting the name and namespace right isn't enough on its own. Every check must report `PASS`. A `PROCTOR: PASS` verdict and exit code `0` mean you're done. Re-submit as many times as you like — this command is non-destructive and does not affect your score history.

When finished, tear the cluster down:

```sh
astrona destroy -c .
```

---

Want the answer walked through instead of attempting it cold? See the [step-by-step guide](./step-by-step-guide.md). Prefer a softer, hint-driven version of this same task? See the [case study](./case-study.md).
