# Grading (the Proctor)

A student never grades their own work — the same way a real exam is graded by a proctor, not by the person taking it. `astrona submit` hands the running environment to the **Proctor**, a single component (`internal/proctor`) that both the student-facing `submit` and the lab-developer-facing `test` command go through. No other command reads `validation.checks`/`validation.script` directly.

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
| `podReady` | `kubectl --context <ctx> wait --for=condition=Ready --timeout=60s <resource>` | exit code 0 within 60s |
| `command` | the given shell words directly (not through a shell) | exit code 0, and every matcher set holds on trimmed stdout |
| `jsonpath` | `kubectl --context <ctx> get <resource> -o jsonpath=<jsonpath>` | every matcher set holds on the value |
| `count` | `kubectl --context <ctx> get <resource> -o name` | the number of objects is within `min` / `max` |
| `http` | `GET <url>` (10s timeout) | status is `expectStatus` (default 200), and every matcher set holds on the body |

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

`resourceExists`/`podReady` always run against the lab's own kubectl context — they require a `kind` runtime (or any runtime with a kubectl-reachable cluster). On a kind lab, `command` checks (and every script) run with `KUBECONFIG` set to the lab's own kubeconfig, so a plain `kubectl ...` grades the lab's cluster regardless of the student's current-context.

### Script checks (`validation.script` / `validation.scripts`)

Beyond existence checks — for verifying actual content, behavior, or anything a `kubectl get` can't express. `script` (singular) runs first if set, then every entry in `scripts` (plural), in order. Exit code `0` is a pass, non-zero is a fail — a failing lab is a normal graded outcome, not a tool error.

For a `qemu` runtime, scripts run over SSH inside the VM instead of on the host. For a multi-VM lab, the shared root `validation` block runs once per VM (each result suffixed `(vmName)` in the report), and each VM's own nested `validation` block (if set) runs after that, scoped to just that VM.

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

## Live grading while you work

```sh
astrona submit --watch            # re-grade every 5s (--interval to change, min 2s)
```

Redraws a compact board — ✓/✗ per check, hints, score, exam time — every few seconds until Ctrl-C, so you see a check flip to ✓ the moment your fix lands. Watch runs are **not recorded** as attempts; run a plain `astrona submit` when you're done. In watch mode validation scripts' output is hidden and `podReady` checks give up after 2s instead of waiting a minute. When the output isn't a terminal, a new board is printed only when a result changes.

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
- `astrona submit --history` adds a TIME USED column for exam attempts, marking over-time ones.
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
