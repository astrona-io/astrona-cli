# Lab Lifecycle

A lab moves through four stages, each an optional block in `config.yaml`. Every command only runs the stages relevant to it — `astrona run` never touches `testing` or `validation`, `astrona submit` never touches `bootstrap`.

```
astrona run          astrona submit           astrona destroy
─────────────►  ┌──────────────┐  ────────►  ┌─────────────┐
 bootstrap       │  (your work)  │   Proctor    teardown
                 └──────────────┘   grading

astrona test  ──────────────────────────────────────────────►
 bootstrap  →  testing  →  submit (Proctor grading)  →  teardown (always)
```

## `bootstrap`

Runs on `astrona run` and at the start of `astrona test`. Two parts, both optional:

```yaml
bootstrap:
  init:
    - name: "setup"
      type: "file"      # "file" | "folder" | "url"
      source: "setup.sh"
  manifests:
    - name: "base"
      type: "folder"    # "file" | "folder" | "url"
      source: "manifests/"
```

- `init` — scripts run in order, through whichever executor the runtime provides (host bash for `kind`, SSH into the VM for `qemu`). A `folder` source runs every file inside in filename order — number them (`01-x.sh`, `02-y.sh`) to control ordering.
- `manifests` — applied with `kubectl apply` against the cluster's context. Requires a `kind` runtime (or a runtime with a kubectl-reachable cluster) — `astrona run` errors out immediately if `bootstrap.manifests` is set on a runtime with none.

## `testing`

Same shape as `bootstrap` (`init` + `manifests`), but only ever runs as part of `astrona test`, never `astrona run`. This is where a lab author puts the **reference solution** — the manifests/scripts that drive the cluster into the "lab completed" state, so `astrona test` has something real to grade. A student taking the lab never sees this stage; it exists so a lab author's CI can prove their own lab is solvable and passes the Proctor's checks before publishing it.

## `validation`

Not a "run" stage — it's what the [Proctor](grading.md) executes when you run `astrona submit` (or as the grading step inside `astrona test`). Declarative `checks` plus one or more custom pass/fail `script`s.

## `teardown`

Runs on `astrona destroy`, and always (even on failure) at the end of `astrona test`.

```yaml
teardown:
  init:
    - name: "dump-logs"
      type: "file"
      source: "teardown/dump-logs.sh"
  keepCluster: false
```

- `init` scripts run first — same `file`/`folder`/`url` shape as `bootstrap.init`, best-effort (a failing teardown script only warns, it never blocks the cluster from being deleted).
- `keepCluster: true` skips deleting the environment afterwards — useful while iterating on a lab locally, since `astrona destroy` re-run without it will still clean up.

## Source types, everywhere

Every script/manifest reference (`bootstrap.init`, `bootstrap.manifests`, `testing.*`, `teardown.init`, `validation.script`) shares the same `ResourceItem` shape:

```yaml
- name: "human-readable label"
  description: "optional, printed before the step runs"
  type: "file"      # or "folder" or "url" (manifests: "file" | "folder" | "url")
  source: "path/or/URL"
```

`url` sources are downloaded to a size-capped temp file before running — `https://` only for the lab config itself, and every download has an explicit byte cap so a misbehaving remote can't exhaust disk.

## Next

[Grading](grading.md) covers exactly what the Proctor checks and how `astrona submit`/`astrona test` report the result.
