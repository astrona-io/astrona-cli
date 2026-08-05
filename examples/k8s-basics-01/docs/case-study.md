# Case Study: k8s-basics-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.caseStudy` — that field is the canonical pointer to this file.

Check the [prerequisites](./prerequisites.md) first. Want the formal, no-hints version of this same task instead? See the [exam question](./exam-question.md). Otherwise, try this before looking at the [step-by-step guide](./step-by-step-guide.md).

## Scenario

You've been handed a fresh local Kubernetes cluster for onboarding. The platform team already wired up the basics — check `../config.yaml`'s `bootstrap` section, it runs `hello.sh` on cluster start so you know the lab loaded correctly.

Your task: bring the cluster into the state the lab expects to see. You won't be told the exact commands — that's the point. You do have the grading criteria, because real labs don't hide how they're graded: it's the whole `validation` block in [`../config.yaml`](../config.yaml) — both `validation.checks` (existence checks) and `validation.script` (a script that inspects actual content).

At the time of writing, that block requires:

- a namespace called `lab-ns`
- a `ConfigMap` called `hello-config`, living inside `lab-ns`
- that `ConfigMap` must have specific data in it — `validation.script` points at `validate.sh`, which is plain bash; open it if you want to know exactly what it checks

(Open `config.yaml` yourself and read `validation.checks`/`validation.script` directly — don't take this doc's word for it, it can drift from the file.)

## Rules

- Don't look in `../solution/` — that's the reference answer used by CI (`astrona test`), not for you.
- You may use `kubectl` however you like: imperative commands, or writing your own YAML and `kubectl apply -f`.
- Existence alone isn't enough — `validation.script` (`validate.sh`) checks the `ConfigMap`'s actual content, not just that it's there. Read it; it's a normal bash script, nothing hidden.

## Steps

1. `astrona run -c .` from inside `examples/k8s-basics-01/` — starts the kind cluster and runs bootstrap.
2. Do whatever `kubectl` work you think satisfies the checks above.
3. `astrona submit -c .` — this is "I'm done, submit for grading." You don't grade yourself; the Proctor does. Read the PASS/FAIL output; if something fails, the message under it tells you what `kubectl` said.
4. Iterate until `PROCTOR: PASS`.
5. `astrona destroy -c .` — tears down the cluster once you're done.

Stuck, or want to see it done for you instead of figuring it out? See the [step-by-step guide](./step-by-step-guide.md).
