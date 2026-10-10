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

Runs on `astrona run` and at the start of `astrona test`. Three parts, all optional, run in this order:

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
  waitFor:
    - resource: deploy/web          # rollout complete
    - resource: pod
      selector: app=db              # every matching pod Ready
      timeout: 5m
```

- `init` — scripts run in order (on kind, after the cluster's DNS is up), through whichever executor the runtime provides (host bash for `kind`, SSH into the VM for `qemu`). A `folder` source runs every file inside in filename order — number them (`01-x.sh`, `02-y.sh`) to control ordering.
- `manifests` — applied with `kubectl apply` against the cluster's context. Requires a `kind` runtime (or a runtime with a kubectl-reachable cluster) — `astrona run` errors out immediately if `bootstrap.manifests` is set on a runtime with none.
- `waitFor` — readiness gates, checked in order after `manifests`. `kubectl apply` returns as soon as the API server accepts the objects, while pods may still be pulling images; without a gate, "ready" only means "applied". Each gate waits (default 2 minutes) for a rollout to finish or a condition to be met, and retries while its target doesn't exist yet (an operator creating it, a CRD still registering). If a gate times out, `astrona run` fails and prints the target's current state plus the namespace's recent events. kind only. See [`WaitFor`](../reference/lab-config.md#waitfor).

`testing` takes the same `waitFor` list: under `astrona test` it gates grading on the reference solution actually being up, so the Proctor never races pods that are still starting.

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
- If a kind lab's environment is already gone, the scripts still run on the host for host-side cleanup — with `KUBECONFIG=/dev/null`, so a script's `kubectl` can never reach your own current context instead of the lab. A qemu lab's scripts only ever run inside its VMs: if the VMs are gone or unreachable, they're skipped with a warning.
- A remote lab (`--git`, URL, catalog) must be [approved](../guides/remote-config.md#approving-remote-labs) before `astrona destroy` runs its teardown scripts, just like `run`. If it isn't (you decline, or there's no terminal and no `--trust`), the scripts are skipped with a warning and the lab is still destroyed.
- Linked clusters (`runtime.kind.clusters`) can have their own `teardown.init`; they run after the lab's, in reverse start order, each with `KUBECONFIG` pointing at its cluster (skipped if that cluster is gone).
- The lab's linked clusters are destroyed even if destroying the lab itself fails.
- If `astrona run` pointed your kubectl at the lab, destroy switches it back to the context you had before (only while the lab's context is still current — see [kubeconfig isolation](runtimes.md#kubeconfig-isolation)), then forgets what was remembered about the lab (`~/.astrona/labs/<lab>.json`: its config source and lab session).
- `keepCluster: true` skips deleting the environment afterwards — useful while iterating on a lab locally, since `astrona destroy` re-run without it will still clean up.

## Starting over: `astrona reset`

```sh
astrona reset -c ./labs/my-lab          # asks before throwing the lab away
astrona reset -c ./labs/my-lab --yes    # no prompt (required in scripts/CI)
```

`reset` runs the lab's `teardown` scripts, destroys it, and then does everything `astrona run` does — the lab ends up exactly as a fresh `run` leaves it (any changes made while working on it are gone). If the lab isn't running, it's simply created.

- The config is validated **before** anything is destroyed — a broken config aborts with "nothing was reset" and leaves the existing lab as it was.
- `teardown.keepCluster` is ignored: reset always recreates.
- `--soft` keeps the cluster(s) and only puts the lab back: it deletes every namespace created after the platform (kube-system, addons, …) was set up, clears what isn't astrona's from `default`, re-runs the bootstrap and restarts port forwards — about 5 seconds instead of 40. Cluster-wide objects a student created (CRDs, ClusterRoles, …) aren't reverted; use a full reset for those. A lab started by astrona before v0.3 has no baseline yet and needs one full reset first.
- `--cluster <name>` rebuilds just one [linked cluster](../guides/linked-labs.md) of a running lab, leaving the lab and its other clusters as they are.
- In a terminal it asks for confirmation; without a terminal it refuses unless `--yes` is passed.
- A full reset of a [catalog lab](../guides/lab-catalog.md#your-astrona-account) needs `astrona login`, like `run`: it starts a fresh lab session and opens the new lab page once the lab is rebuilt. `--soft` and `--cluster` keep the current session and need no sign-in.

**`astrona run` on a lab that's already running** asks the same question: *destroy it and start over?* — yes does what `reset` does, no keeps the lab as it is. `astrona run --yes` starts over without asking; without a terminal, `run` on a running lab stops and names `--yes`. If **other** labs are running, `run` also offers to destroy them first (handy when moving from one lab of a training to the next) — a multi-VM qemu lab counts as one lab, all its VMs destroyed and the lab forgotten together, as `astrona destroy <lab>` does — only when asked in a terminal; `--yes` never touches other labs.

To pause a lab instead of losing it, see [`astrona stop` / `astrona start`](runtimes.md#pausing-a-lab-astrona-stop-astrona-start).

## Source types, everywhere

Every script/manifest reference (`bootstrap.init`, `bootstrap.manifests`, `testing.*`, `teardown.init`, `validation.script`) shares the same `ResourceItem` shape:

```yaml
- name: "human-readable label"
  description: "optional, printed before the step runs"
  type: "file"      # or "folder" or "url" (manifests: "file" | "folder" | "url")
  source: "path/or/URL"
```

`url` sources — scripts and manifests alike — are downloaded to a size-capped temp file before running or applying (a manifest URL is never handed to `kubectl apply -f <url>` directly). Every remote download (scripts, manifests, the lab config, QEMU base images, astrona release binaries) is `https://` only, and that includes redirects: a redirect to a plain `http://` URL is refused rather than followed. Every download also has an explicit byte cap so a misbehaving remote can't exhaust disk.

## Command output

`astrona run`, `test`, `submit`, and `destroy` render progress as a compact
per-phase step view — one line per step (`✓` done, `✗` failed, `-` skipped)
with its elapsed time, grouped under section headers (`Bootstrap`,
`Machine: <name>` for a multi-VM qemu lab, and so on). On an interactive
terminal the in-progress step shows a spinner.

The raw output of every underlying command (`kind`, `qemu`, `kubectl`, each
bootstrap script) is **not** shown by default. Instead:

- It is always written in full to a per-run log file at
  `~/.astrona/logs/<command>-<lab>-<timestamp>.log`. The path is printed at
  the end of a run. Browse past runs with [`astrona logs`](logs.md).
- If a step fails, the tail of that step's output is printed inline right
  before the error, and the command exits non-zero.
- Passing `--verbose` streams everything live as it happens and disables the
  spinner — the pre-step-view behaviour. The log file is still written.

Non-interactive output (piped, redirected, or CI) automatically drops the
spinner and ANSI styling and prints one plain line per step.

On a terminal, problems stand out in color: `Error:` and what went wrong in
red (hints on what to do next follow in the normal color), `[WARN]` in yellow,
`[INFO]` in cyan, and grading results as green `PASS` / red `FAIL` with a
yellow `hint:`. Set `NO_COLOR=1` to turn colors off everywhere; piped and
redirected output never has them.

When a step fails, its error and the tail of its output are printed with the
path of the full log — `astrona logs view` opens it.

### Questions astrona asks

| Question | Answer it up front with |
|---|---|
| Throw a lab away? (`reset`, `logs clean`, `run` on a running lab) | `--yes` / `-y` |
| Run a lab from a URL, git repo or bundle? (shows what it will do) | `--trust` |
| Download and install the astrona version a lab needs? | `--install-version` |

The last two approve running someone else's code, so they have their own
flags rather than a general `-y`. Without a terminal astrona never waits for
an answer: it stops and names the flag.

For a `qemu` lab, a successful `astrona run` also prints a **Connect:** block
with the ready-to-paste `astrona ssh <name>` command for each VM.

For a `kind` lab with [`runtime.portForwards`](runtimes.md#port-forwards), `astrona run` starts those forwards after
`bootstrap` and prints a **Port forwards:** block with each forward's local
URL and status (`Ready`/`NotReady`/…). `astrona port-forward list` shows the
same status any time later.

## Next

[Grading](grading.md) covers exactly what the Proctor checks and how `astrona submit`/`astrona test` report the result.
