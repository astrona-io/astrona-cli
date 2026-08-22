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
| `command` | the given shell words directly (not through a shell) | exit code 0, and (if `expect` is set) trimmed stdout equals `expect` exactly |

`resourceExists`/`podReady` always run against the lab's own kubectl context — they require a `kind` runtime (or any runtime with a kubectl-reachable cluster).

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

## JUnit XML for CI

Both `astrona submit` and `astrona test` accept `--junit-xml=<path>`:

```sh
astrona submit --junit-xml=report.xml
```

Writes a standard JUnit XML report alongside the terminal output — GitHub Actions, GitLab CI, Jenkins, and most other CI systems render this natively as a test report. See [CI Integration](../guides/ci-integration.md).

## `submit` vs `test`

|  | `astrona submit` | `astrona test` |
|---|---|---|
| Who runs it | a student, on a lab they already `run` | a lab author, in CI, proving their own lab |
| Applies `testing.*` (reference solution) first | No | Yes |
| Tears down afterwards | No (you still own the environment) | Always, even on failure |
| Cluster name prefix | `astro-` | `astro-test-` (never collides with a real `astrona run`) |
