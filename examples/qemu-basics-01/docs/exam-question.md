# Exam Question: qemu-basics-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.question`. Make sure you meet the [prerequisites](./prerequisites.md) first.

**Task weight: 100%** (this lab has a single scored task)

---

## Context

A real VM has been booted for you with `qemu-system-x86_64` (not a container) and provisioned with an ephemeral, lab-only SSH key. There is no Kubernetes cluster and no `kubectl` context here — everything happens by executing on the VM directly over SSH.

## Task

Perform the following, on the VM (not on your own machine):

1. Create the directory:
   ```
   /tmp/astrona-lab
   ```

2. Write a file at:
   ```
   /tmp/astrona-lab/marker.txt
   ```
   containing exactly:
   ```
   qemu-smoke-ok
   ```

You may do this any way you like — the point of this lab is proving you can get a command to actually run inside the VM, not the specific mechanism.

## Constraints

- Do not modify `config.yaml` or anything under `docs/` in this lab directory. Grading reads `config.yaml` as-is.
- Do not tear down the VM mid-task — start it once with `astrona run` and work against that VM until you submit.
- Work as the `student` user (`astrona ssh qemu-basics-01` logs you in as it); bootstrap does not create anything under `/tmp/astrona-lab` for you.

## How you will be graded

You do not grade yourself. From the `examples/qemu-basics-01/` directory, submit your work to the Proctor:

```sh
astrona submit -c .
```

The Proctor's `validation.script` (`validate.sh`) runs inside the VM over SSH and checks the marker file's exact content — so creating *a* file isn't enough, it has to be the right path with the right content. A `PROCTOR: PASS` verdict and exit code `0` mean you're done.

A submit is final: pass or fail, it records the attempt, ends it and deletes the VM — there is nothing left to tear down, and another try means a fresh `astrona run`. To check your work first without ending the attempt, run:

```sh
astrona submit -c . --watch
```

It re-grades while you work and records, sends and deletes nothing; Ctrl-C stops it.

---

Want the answer walked through instead of attempting it cold? See the [step-by-step guide](./step-by-step-guide.md). Prefer a softer, hint-driven version of this same task? See the [case study](./case-study.md).
