# Case Study: qemu-basics-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.caseStudy` — that field is the canonical pointer to this file.

Check the [prerequisites](./prerequisites.md) first. Want the formal, no-hints version of this same task instead? See the [exam question](./exam-question.md). Otherwise, try this before looking at the [step-by-step guide](./step-by-step-guide.md).

## Scenario

You've been handed a freshly booted VM, not a container. The platform team already wired up bootstrap — check `../config.yaml`'s `bootstrap` section, it runs `hello-vm.sh` on boot so you know the VM is actually reachable over SSH.

Your task: get the VM into the state the lab expects to see. You won't be told the exact commands — that's the point. You do have the grading criteria, because real labs don't hide how they're graded: it's the whole `validation` block in [`../config.yaml`](../config.yaml) — this lab only uses `validation.script` (there's no `validation.checks` here, since there's no kubectl target to check against).

At the time of writing, that script requires:

- a file at `/tmp/astrona-lab/marker.txt`
- containing exactly `qemu-smoke-ok`

(Open `config.yaml` yourself and read `validation.script` directly, then open `validate.sh` — don't take this doc's word for it, it can drift from the file.)

## Rules

- This lab has no `../solution/` reference folder (unlike the kind-based labs) — there's nothing to peek at.
- Everything runs inside the VM, not on your own machine — `astrona` always executes bootstrap/validation there over SSH.
- Existence alone isn't enough — `validate.sh` checks the marker file's exact content, not just that it's there. Read it; it's a normal bash script, nothing hidden.

## Steps

1. `astrona run -c .` from inside `examples/qemu-basics-01/` — boots the VM and runs bootstrap over SSH.
2. Do whatever work you think satisfies the check above (SSH in yourself using the same key `astrona` generated, or just trust that bootstrap already did it — see the [step-by-step guide](./step-by-step-guide.md) for how to connect manually).
3. `astrona submit -c .` — this is "I'm done, submit for grading." You don't grade yourself; the Proctor does, running `validate.sh` inside the VM. Read the PASS/FAIL output.
4. Iterate until `PROCTOR: PASS`.
5. `astrona destroy -c .` — tears down the VM once you're done.

Stuck, or want to see it done for you instead of figuring it out? See the [step-by-step guide](./step-by-step-guide.md).
