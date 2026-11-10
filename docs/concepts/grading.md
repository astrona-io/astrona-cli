# Grading (the Proctor)

A student never grades their own work — the same way a real exam is graded by a proctor, not by the person taking it. `astrona submit` hands the running environment to the **Proctor**, a single component (`internal/proctor`) that both the student-facing `submit` and the lab-developer-facing `test` command go through. No other command reads `validation.checks`/`validation.script` directly.

If you restart an existing lab through the UI's discard confirmation, the new
run is local only. `astrona submit` still grades it and records the local result,
but does not upload it to the Astrona API, even if you are signed in. This choice
is saved with the run and also applies after reopening the CLI.

## What it checks

```yaml
validation:
  checks:
    - name: "lab-ns namespace exists"
      type: "resourceExists"
      resource: "namespace/lab-ns"
    - name: "pod is ready"
      type: "podReady"
      resource: "pod/my-pod -n lab-ns"
    - name: "custom command check"
      type: "command"
      command: "kubectl get deploy my-app -o jsonpath={.status.readyReplicas}"
      expect: "3"
  script:
    name: "verify-configmap-content"
    type: "file"
    source: "validate.sh"
  scripts:
    - name: "check-network"
      type: "file"
      source: "validate-network.sh"
```

### Declarative checks (`validation.checks`)

| `type` | Runs | Passes when |
|---|---|---|
| `resourceExists` | `kubectl --context <ctx> get <resource>` | exit code 0 |
| `podReady` | `kubectl --context <ctx> wait --for=condition=Ready --timeout=60s <resource>` (or the check's `timeout`) | exit code 0 within 60s |
| `command` | the given shell words directly (not through a shell) | exit code 0, and every matcher set holds on trimmed stdout |
| `jsonpath` | `kubectl --context <ctx> get <resource> -o jsonpath=<jsonpath>` | every matcher set holds on the value |
| `count` | `kubectl --context <ctx> get <resource> -o name` | the number of objects is within `min` / `max` |
| `http` | `GET <url>` (10s timeout) | status is `expectStatus` (default 200), and every matcher set holds on the body |

**Time limits**: every check runs for at most `1m` (`http`: `10s`; `podReady`: its 60s wait) unless it sets `timeout` (e.g. `timeout: 2m`). A check that runs past it is stopped, together with everything it started, and fails with `timed out after …` — it earns no points, and the rest of the lab is still graded. See [Time limits](../reference/lab-config.md#time-limits).

**Matchers** (for `command`, `jsonpath`, `http`): `expect` (exact), `contains`, `expectRegex` — set any combination; all must hold.

```yaml
validation:
  checks:
    - name: scaled to 3
      type: jsonpath
      resource: deploy/web -n shop
      jsonpath: "{.spec.replicas}"
      expect: "3"
    - name: image pinned
      type: jsonpath
      resource: deploy/web -n shop
      jsonpath: "{.spec.template.spec.containers[0].image}"
      expectRegex: "^nginx:1\\.27"
    - name: at least 3 web pods
      type: count
      resource: pods -l app=web -n shop
      min: 3
    - name: site answers through the gateway
      type: http
      url: http://web.localtest.me:8080/
      contains: Welcome to nginx
```

A failed check says what it found: `{.spec.replicas} is "2" — want exactly "3"`, `found 2, want at least 3`, `GET … → 404, want 200`. `http` checks reach the lab through a [port forward](runtimes.md#port-forwards) or the [gateway addon](runtimes.md#addons-runtimekindaddons). `astrona validate` checks each check has the fields its type needs.

`resourceExists`, `podReady`, `jsonpath` and `count` run `kubectl` on your machine, and only ever against the lab's own isolated kubeconfig and context (or a [linked cluster's](../guides/linked-labs.md)) — never your own `~/.kube/config`. So they need a `kind` lab: `astrona validate` rejects them in a `qemu` lab, which has no cluster for kubectl (use a validation script, which runs inside the VM), and grading stops with an error (exit 1) if a running lab has no kubeconfig of its own (a kind lab started by an older astrona — `astrona reset` fixes it). `resource` may only carry the flags `-n`/`--namespace`, `-l`/`--selector`, `--field-selector` and `-A`/`--all-namespaces`; any other flag (`--kubeconfig`, `--server`, `-o`, …) or a value starting with `-` is rejected. Before the first of these checks, the Proctor makes sure the cluster answers: an unreachable cluster is a grading error (exit 1, nothing recorded or sent), not a failed check.

On a kind lab, `command` checks (and every script) run with `KUBECONFIG` set to the lab's own kubeconfig, so a plain `kubectl ...` grades the lab's cluster regardless of the student's current-context. They also get `ASTRONA_KUBE_CONTEXT`, `ASTRONA_KIND_CLUSTER` and `ASTRONA_CONTAINER_ENGINE`, so a check can reach the lab's node containers without hardcoding the lab name (see [runtimes](runtimes.md#kind-default)).

### Script checks (`validation.script` / `validation.scripts`)

Beyond existence checks — for verifying actual content, behavior, or anything a `kubectl get` can't express. `script` (singular) runs first if set, then every entry in `scripts` (plural), in order. Exit code `0` is a pass, non-zero is a fail — a failing lab is a normal graded outcome, not a tool error. A validation script may run for `5m` unless it sets `timeout`; one that runs longer is killed, with every process it started, and fails with `timed out after 5m`.

For a `qemu` runtime, scripts run over SSH inside the VM instead of on the host. Exit code `255` is reserved there: it's what `ssh` exits with when it can't reach the VM, so it's treated as "grading couldn't run" (exit 1, nothing recorded or sent), not as a fail — a script must not `exit 255` to mean "fail". For a multi-VM lab, the shared root `validation` block's scripts run once per VM (each result suffixed `(vmName)` in the report; root checks run once), and each VM's own nested `runtime.qemu[].validation` block (if set) is graded after that, scoped to just that VM: its checks, then its scripts, each result suffixed `(vmName)` too (and, in `submit -o json`, carrying `"vm": "<vmName>"`). A single-VM lab grades its one VM's nested block after the root one. A `command` check in a VM's own block runs **inside that VM** over SSH, like a script (its words quoted, never interpolated), while a root `command` check runs on this machine; an `http` check runs from this machine in either. Only the root `validation.passPercent` sets the pass mark: `astrona validate` (and `run`) reject a `passPercent` in a VM's own block.

## Reading the output

Output is pytest/robot-style — one line per check, then a summary:

```
  PASS  lab-ns namespace exists (0.18s)
  PASS  hello-config configmap exists (0.09s)
  FAIL  verify-configmap-content (0.31s)
        expected value "hello-world", got "unset"

2 passed, 1 failed in 0.58s

PROCTOR: FAIL
```

The command's exit code reflects the verdict — `submit`/`test` exit non-zero on a FAIL, so both are safe to gate a script or CI job on.

A lab with nothing to grade — no `validation.checks` and no validation scripts — never passes: `submit` and `test` stop with an error (exit 1) and record nothing. `astrona validate` warns about it; that's fine for an ungraded playground, which is only ever run.

## Hints, score and attempts

Each check and validation script can carry a `hint` — shown only when it fails — and `points` (default `1`):

```yaml
validation:
  passPercent: 75            # optional: pass at 75% instead of requiring every check
  checks:
    - name: namespace shop exists
      type: command
      command: kubectl get namespace shop -o name
      expect: namespace/shop
      points: 3
      hint: "Namespaces are cluster-scoped — kubectl create namespace <name>"
```

```text
  FAIL  configmap app-config in shop (0.03s)
        hint: A ConfigMap lives in a namespace — did you pass -n shop?

1 passed, 1 failed in 0.15s
Score: 3/4 points (75%) — pass mark 75%

Attempt #2: 3/4 (+3 point(s) since #1)
  now passing: namespace shop exists

PROCTOR: PASS
```

- Write hints as nudges toward the concept, not the answer — the `guide` doc has the full solution.
- **Pass rule:** without `passPercent`, every check and script must pass (as before). With it, the submission passes once the weighted score reaches it — like a certification exam's pass mark.
- `astrona submit --no-hints` grades without showing hints (exam conditions).
- Every `astrona submit` is recorded in `~/.astrona/results/<lab>.jsonl`; each submission shows the change since the previous one, and `astrona submit --history` lists all attempts with the best score. `astrona test` (CI) doesn't record attempts.
- `astrona submit` with no lab named — no argument, nothing picked with `astrona use`, no lab config in the current directory — grades the only running lab (`Submitting astro-my-lab (the only running lab).`), found again through what `astrona run` remembered. With several running it lists them, each with the command that names it.
- A [catalog lab](../guides/lab-catalog.md#your-astrona-account) started while signed in also sends each result to its lab page. Sending never changes the exit code.
- **A submit is final.** Every recorded result, PASS or FAIL, ends the attempt and stops its timer, and then submit deletes the lab, as `astrona destroy` does: once the result was sent (or queued because Astrona couldn't be reached — it goes out later under the same key, never twice), or, for a run without a lab session (local-only, a lab from your own files), once it is saved here. There is no option to keep the lab; `teardown.keepCluster` does not apply. Only a tracked result Astrona refuses, or one that can't be sent or queued, leaves the lab. Use `astrona submit --watch` to check your work without ending the attempt.

## Live grading while you work

```sh
astrona submit --watch            # re-grade every 5s (--interval to change, min 2s)
```

Redraws a compact board — ✓/✗ per check, hints, score, exam time — every few seconds until Ctrl-C, so you see a check flip to ✓ the moment your fix lands. Watch runs are **not recorded** as attempts and end nothing: this is how to check your work while the attempt stays open. Run a plain `astrona submit` only when you're done — it ends the attempt and deletes the lab. In watch mode validation scripts' output is hidden and `podReady` checks give up after 2s instead of waiting a minute. When the output isn't a terminal, a new board is printed only when a result changes. With `exam.strict`, once the time limit has passed the board shows "Over the time limit — a submit now would not count as a pass" instead of "Passing", the same rule a plain submit applies. Because nothing is recorded, `--watch` refuses `-o json`, `--junit-xml` and `--history` with an error rather than ignoring them.

## Exam mode

```yaml
exam:
  timeLimit: 2h      # clock starts when `astrona run` has the lab ready
  hideHints: true    # no hints, like the real exam
  strict: true       # a submission after the limit can't pass
validation:
  passPercent: 66    # e.g. a CKA-style pass mark (see above)
```

```text
Score: 9/12 points (75%) — pass mark 66%
Time: 1h12m of 2h0m used (48m left)
PROCTOR: PASS
```

- The clock lives in `~/.astrona/exams/<lab>.json`; `astrona reset` starts a fresh one, `astrona destroy` clears it. It keeps running while a lab is paused with `astrona stop`, like a real exam.
- Without `strict`, an over-time submission is still graded normally — the time is just reported ("over time by 5m").
- `astrona submit --history` adds a TIME USED column, marking over-time exam attempts. A learning lab's time counts from Start in the browser workspace, or from when the lab became ready; `astrona submit` prints it as `Time spent`.
- `astrona test` (CI) ignores exam settings.

## JUnit XML for CI

Both `astrona submit` and `astrona test` accept `--junit-xml=<path>`:

```sh
astrona submit --junit-xml=report.xml
```

Writes a standard JUnit XML report alongside the terminal output — GitHub Actions, GitLab CI, Jenkins, and most other CI systems render this natively as a test report. See [CI Integration](../guides/ci-integration.md).

## Progress across labs

```sh
astrona progress
```

```text
LAB            RESULT        BEST         ATTEMPTS   FASTEST PASS   LAST
k8s-web-01     passed (#2)   5/5 (100%)   3          18m0s          2h ago
net-policy-02  not yet       3/5 (60%)    4          -              1d ago

1 of 2 lab(s) passed.
```

Built from the attempt history every `astrona submit` records: whether and on which attempt you passed, your best score, how many attempts, the fastest passing time for exam labs, and when you last worked on it. `-o json` for export or scripts.

## `submit` vs `test`

|  | `astrona submit` | `astrona test` |
|---|---|---|
| Who runs it | a student, on a lab they already `run` | a lab author, in CI, proving their own lab |
| Applies `testing.*` (reference solution) first | No | Yes |
| Tears down afterwards | No (you still own the environment) | Always, even on failure |
| Cluster name prefix | `astro-` | `astro-test-` (never collides with a real `astrona run`) |
