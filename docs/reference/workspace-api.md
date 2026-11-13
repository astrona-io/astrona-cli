# Workspace API

The browser workspace is a small web page served by `astrona` itself, on
`127.0.0.1` only. This page is the contract between that page and the server
(`internal/workspace`). It describes what the server implements today; routes
marked *later* are reserved and answer `404 not_found` until they exist.

The server is started on demand by `astrona run <lab>` (when you answer yes to
"Open this lab in your browser?") and stopped by `astrona ui stop` or after it
has been idle for a while; `astrona ui <lab>` starts it for a running lab.
See the [browser workspace guide](../guides/browser-workspace.md).

## Conventions

- **Base URL.** `http://127.0.0.1:<port>`. The server prefers port 47300 and
  takes a free port when that one is in use; the port is recorded in
  `~/.astrona/workspace/server.json`. `localhost:<port>` is accepted as well;
  any other `Host` header is refused with `421`.
- **JSON.** Request bodies must be `Content-Type: application/json`, at most
  4 KiB, and may not contain unknown fields. Responses are JSON with
  `Cache-Control: no-store`.
- **Times** are RFC 3339 strings in UTC. Every run response carries
  `serverNow`, so the page can compute remaining time from `deadlineAt` and the
  offset to its own clock instead of trusting the local clock.
- **Errors** always have this shape:

  ```json
  { "error": "human readable message", "code": "not_started" }
  ```

  | Status | `code` | When |
  | --- | --- | --- |
  | 400 | `bad_request` | Malformed body, wrong content type, unknown field, invalid size. |
  | 400 | `unknown_tab` | `tab` is not one of the run's terminal tabs. |
  | 400 | `invalid_idempotency_key` | `POST /submit` without an `Idempotency-Key` of 8 to 128 letters, digits, `-` or `_`. |
  | 401 | `unauthorized` | Missing or invalid token, launch code or ticket. |
  | 403 | `forbidden` | Not a loopback peer, foreign `Origin`, cross-site `Sec-Fetch-Site`. |
  | 404 | `not_found` | Unknown route, lab, question or terminal. |
  | 405 | `method_not_allowed` | Method not allowed (`OPTIONS` included: there is no CORS). |
  | 409 | `run_changed` | `runId` in the request is not the lab's current run. Reload the run. |
  | 409 | `not_started` | Question text or terminals requested before **Start**. |
  | 409 | `run_stopped` | The lab is no longer running (stopped, destroyed or restarted). |
  | 409 | `terminal_starting` | Another request is already starting this tab's terminal. Try again in a few seconds. |
  | 409 | `submit_in_progress` | The lab is already being submitted (with another `Idempotency-Key`), or removal was asked for while it is. |
  | 409 | `no_grading` | `POST /submit` on a playground: it has no checks. End it with `POST /destroy`. |
  | 409 | `lab_busy` | Another astrona command holds the lab's lock. Nothing was graded or sent. |
  | 409 | `not_trusted` | The lab's scripts were never approved on this machine. Approve it with `astrona run` in a terminal. |
  | 409 | `environment_kept` | `POST /destroy` on a lab from your own files with `teardown.keepCluster`. Nothing was removed. |
  | 404 | `no_submission` | `GET /submission` for a run that was never submitted. |
  | 421 | `unexpected_host` | `Host` is not `127.0.0.1:<port>` or `localhost:<port>`. |
  | 429 | `too_many_terminals` | The lab already has 12 open terminals. |
  | 500 | `internal` | Anything else. The message says what failed. |
  | 501 | `unsupported` | Terminals on a platform without a PTY (native Windows). |
  | 502 | `terminal_failed` | The terminal container could not be started. Nothing else was started instead. |
  | 500, 502 | `grading_failed` | The checks could not run (config not loadable, container engine down). Nothing was recorded or sent; the lab keeps running. |
  | 502 | `destroy_failed` | Removing the environment failed. The message says what is left. |
  | 503 | `unavailable` | The server is stopping; no new terminal is started. |

## Authentication

1. The CLI prints and opens a launch URL:

   ```text
   http://127.0.0.1:<port>/#code=<code>&lab=<lab>
   ```

   The fragment is never sent to the server. `code` is single-use and valid for
   two minutes; `lab` names the run the page should show (absent: show the
   list from `GET /api/v1/runs`).

2. The page exchanges the code and removes the fragment from the address bar:

   `POST /api/v1/auth/exchange` `{"code": "<code>"}` → `200 {"token": "<token>"}`

   A used, expired or forged code gives `401 unauthorized`.

3. Every other `/api/v1` request sends `Authorization: Bearer <token>`. There
   are no cookies. Keep the token in `sessionStorage` (origin-scoped, port
   included). A server restart invalidates every token; the page then asks the
   user to run `astrona ui <lab>` for a new link.

4. WebSockets cannot send headers. Fetch a ticket first:

   `POST /api/v1/auth/ticket` (no body) → `200 {"ticket": "<ticket>", "expiresAt": "<time>"}`

   A ticket is single-use and valid for 30 seconds.

5. Entering a run from setup or the lab list loads a fresh document so its
   framing policy includes that run's page tabs:

   `POST /api/v1/auth/code` (bearer token, no body) → `200 {"code": "<code>"}`

   This code has the same two-minute lifetime and single-use rules as a CLI
   launch code. The page reloads with it in the fragment and exchanges it as
   above. This also works when browser storage is disabled and the current
   bearer token exists only in memory; bearer tokens never go in URLs.

## Routes

All paths are under `/api/v1` unless they start with `/ws/`.

| Method and path | Purpose |
| --- | --- |
| `POST /auth/exchange` | Launch code → session token. |
| `POST /auth/code` | Authenticated page → single-use launch code for document navigation. |
| `POST /auth/ticket` | Single-use WebSocket ticket. |
| `GET /server` | Version, profile, features. |
| `GET /launches/{id}` | A browser-first `astrona run` before its lab exists: terms, setup progress, outcome. |
| `POST /launches/{id}/terms` | Accept or decline the CLI terms for that run. |
| `GET /labs` | Every lab on this computer, opened in the browser or not. |
| `GET /runs` | Labs the workspace was opened for. |
| `GET /runs/{lab}` | One run: identity, mode, status, clock, questions, targets, tabs. |
| `GET /runs/{lab}/questions/{id}` | One question rendered to HTML. After **Start** only. |
| `POST /runs/{lab}/start` | The **Start** button. Idempotent. |
| `GET /runs/{lab}/terminals` | Open terminals. |
| `GET /runs/{lab}/tabs/{name}/doc` | A docs tab's Markdown, rendered to HTML. After **Start** only. |
| `GET /runs/{lab}/doclinks/frame?url=…` | Whether one of the run's `docLinks` can be shown in a frame. |
| `GET /runs/{lab}/solution` | The solution guide, rendered to HTML. After **Start** only; never in an exam or a playground. |
| `POST /runs/{lab}/terminals` | Open the shell of a terminal tab, or of a new terminal on a target. After **Start** only. |
| `POST /runs/{lab}/terminals/close` | End the shell of a terminal tab the learner closed. |
| `POST /runs/{lab}/submit` | Grade the run, record and send the result. Needs an `Idempotency-Key` header. |
| `GET /runs/{lab}/submission` | The run's latest submission, or that one is being graded. |
| `POST /runs/{lab}/destroy` | Remove the environment without grading: ends a playground, or removes a lab as `astrona destroy` does. |
| `GET /ws/v1/terminals/{id}?ticket=…` | Attach to a terminal (WebSocket). |
| *later* `PUT /runs/{lab}/progress`, `GET /runs/{lab}/events` | Not implemented yet. |

GET routes never change saved state, so polling them is safe.

### `GET /server`

```json
{
  "version": "0.5.0",
  "profile": ".astrona",
  "startedAt": "2026-11-09T21:00:00Z",
  "serverNow": "2026-11-09T21:05:12Z",
  "features": ["runs", "terminals", "tabs", "submit"],
  "assets": "built"
}
```

`assets` is `"placeholder"` when the binary was built without the frontend.

### `GET /launches/{id}`

A launch is one interactive `astrona run` that opened the browser before its
lab exists. The page finds it from `&launch=<id>` in the link (it rewrites the
address to `#/launches/<id>`) and polls this route. `astrona run` writes the
launch; the server only reads it, and notes when the page last asked so the
command knows whether the tab is still open.

```json
{
  "schema": 1,
  "id": "3f9c…",
  "pid": 41234,
  "stage": "preparing",
  "title": "Kubernetes playground",
  "events": [
    {"at": "…", "kind": "section", "label": "Cluster"},
    {"at": "…", "kind": "step", "label": "Create kind cluster"},
    {"at": "…", "kind": "ok", "label": "Create kind cluster", "seconds": 41.2}
  ],
  "status": "waiting for CoreDNS · 12s",
  "logPath": "~/.astrona/logs/…",
  "account": { "state": "signed-in", "username": "ada", "site": "https://astrona.io" },
  "alive": true
}
```

| `stage` | Meaning |
| --- | --- |
| `terms` | Waiting for the terms. `terms` holds `revision`, `sha256` and `text`, exactly as the running CLI ships them. |
| `checking` | Resolving the lab. The terminal may ask a question. |
| `preparing` | Building the lab; `events` and `status` show progress. |
| `ready` | `lab` names the run the page opens next (`GET /runs/{lab}`), after **Begin this session**, which also starts it (`POST /runs/{lab}/start`) without showing the intro. |
| `failed` | `error` says why. |
| `declined` | The terms were declined; nothing was started. |
| `ended` | The command stopped without a lab for this page; `message` says why. |

`alive` is false once the `astrona run` process has exited; with a stage that
is not final, the command was stopped. `answered` (`accepted` or `declined`)
appears once the terms were answered. At most 200 events are kept. Launches
older than a day are removed.

`account` appears once `astrona run` has decided whose account the run is
saved to, which is after the lab was resolved and the lab session was (or was
not) started. The page shows it as a card while the lab is set up.
`playground: true` is set with it when the lab is a playground, which has time but
no attempts, so the card does not mention a result.

| `account.state` | Meaning |
| --- | --- |
| `signed-in` | A catalog lab with a lab session: the time and the submitted result, pass or fail, are saved to the account (a playground: the time). `username` and `site` say which account. |
| `signed-out` | A catalog lab started without a lab session (not signed in): nothing from this run is saved to an account. `site` is the site astrona would sign in to, for a sign-up link. |
| `local-only` | Started with `--local-only`: nothing is sent. |
| `untracked` | A lab from local files or `--git`: no account applies. The page shows nothing. |

`account` only ever holds `state`, `username` and `site`, never a token,
device ID or key ID. `site` is the site the CLI resolved itself (`ASTRONA_URL`,
then the saved sign-in, then the profile default), never a value from lab
content. The state describes this run: signing in later does not change it.

Errors: `404 launch_gone` (removed, or never existed).

### `POST /launches/{id}/terms`

Request: `{"accept": true, "sha256": "<terms.sha256>"}`. Writes the answer
for the waiting command, which saves the receipt (method `browser`) or stops.

Response: `200 {"accepted": true}`.

Errors: `404 launch_gone`; `409 not_asking` (not at `terms`, or the command
exited); `409 terms_changed` (the digest does not match); `409 answered`.

### `GET /labs`

Every astrona lab on this computer: Kind clusters and QEMU VMs with this
build's prefix, joined with their saved state. Linked clusters (a lab's second
cluster) and test clusters are left out.

```json
{
  "labs": [
    { "lab": "astro-pod-basics", "name": "pod-basics", "title": "Pod basics",
      "runtime": "kind", "state": "running", "uptime": "2h", "mode": "learning",
      "playground": false, "workspace": true, "runId": "…", "runStatus": "ready" }
  ],
  "engine": "…"
}
```

`state` is `running`, `stopped`, `degraded` (some VMs stopped) or `unknown`.
`vms` counts a QEMU lab's VMs. `deadlineAt` is when a playground is deleted or
an exam ends. `workspace` is true when the page has a current run for the lab
(`GET /runs/{lab}`); `title`, `mode`, `runId` and `runStatus` come from that
run. `engine` is set, with a message, when the container engine does not
answer; Kind labs are then missing from the list.

### `GET /runs`

```json
{
  "runs": [
    { "lab": "pod-basics", "title": "Pod basics", "mode": "learning",
      "playground": false, "status": "ready" }
  ]
}
```

### `GET /runs/{lab}`

```json
{
  "lab": "pod-basics",
  "runId": "3f1c…64 hex characters…",
  "title": "Pod basics",
  "mode": "exam",
  "playground": false,
  "status": "running",
  "startedAt": "2026-11-09T21:01:00Z",
  "deadlineAt": "2026-11-09T22:31:00Z",
  "timeLimitSeconds": 5400,
  "serverNow": "2026-11-09T21:05:12Z",
  "rules": { "strict": true, "hintsHidden": true },
  "intro": {
    "prerequisitesHtml": "<p>Know what a Pod is.</p>\n",
    "estimatedDuration": "45m"
  },
  "questions": [
    { "id": "q1", "title": "Pod basics", "points": null }
  ],
  "targets": [
    { "name": "pod-basics", "label": "main", "kind": "kind", "views": ["terminal"] }
  ],
  "tabs": [
    { "name": "main", "title": "Terminal", "kind": "terminal", "focus": true, "target": "pod-basics" },
    { "name": "shop", "title": "Shop", "kind": "browser", "focus": false, "url": "http://127.0.0.1:8080/" },
    { "name": "k8s", "title": "Kubernetes docs", "kind": "docs", "focus": false,
      "url": "https://kubernetes.io/docs/concepts/workloads/pods/" },
    { "name": "notes", "title": "Notes", "kind": "docs", "focus": false, "document": true }
  ],
  "docLinks": [],
  "canSubmit": true,
  "submit": {
    "tracking": "account",
    "grading": false,
    "submitted": false,
    "blocked": ""
  },
  "account": { "state": "signed-in", "username": "ada", "site": "https://astrona.io" },
  "runAgain": "astrona run astrona.io/ATS014/section-010/module-01/lab-02"
}
```

| Field | Meaning |
| --- | --- |
| `runId` | Opaque. Send it back on every mutating request. It changes when the lab is restarted, reset to a new session or run from another config. |
| `mode` | `exam` (the lab has `exam.timeLimit`) or `learning`. |
| `playground` | A playground has no intro and no Start: open the workspace directly. |
| `status` | `ready` (environment up, Start not clicked), `running`, `expired` (exam time is over), `stopped` (the lab is gone or was restarted; this `runId` is dead). `provisioning` and `failed` are reserved for later milestones. |
| `startedAt` | When Start was clicked; `null` before. |
| `deadlineAt` | `null` when there is no clock: learning labs, exams before Start, untimed playgrounds. A playground started from the catalog has its session deadline. |
| `timeLimitSeconds` | The exam's time limit, shown on the intro; `null` for learning labs. |
| `rules` | `strict`: a submission after the deadline cannot pass. `hintsHidden`: hints are never shown. |
| `intro` | For the intro page. `prerequisitesHtml` is `""` when the lab has none; `estimatedDuration` is `""` when its task does not say. |
| `questions` | Summaries only, never the text. Today every lab has exactly one question, `q1`, built from `metadata.docs.question`. `points` is `null` until per-question points exist. |
| `targets` | Where terminals run. `kind` is `kind` (a Kind cluster) or `vm` (one VM of a QEMU lab; `name` is the VM's handle, the lab name for a single-VM lab or `<lab>-<vm>`). `label` is the display name (`main`, a linked cluster's name, or the VM's name in a multi-VM lab). `views` lists what the target offers: `["terminal"]` today; a desktop will add `"desktop"`. |
| `tabs` | The right panel, as the lab declares it (lab config `tabs`). The first is always the focus tab: `name` `main`, a terminal on the lab's own cluster. The page opens with these; the learner can close them and open more from the tab bar's **+** menu (another terminal on a target, the task, the prerequisites, the solution guide, a closed lab tab), which the page remembers per run in `localStorage`. `kind` is `terminal` (`target` says where its shell runs), `browser` (`url` is a loopback port forward of the lab), or `docs`: either `url` (an `https` documentation page, shown in a frame) or `document: true` (Markdown shipped with the lab, fetched from `GET /tabs/{name}/doc`); `solution: true` marks a docs tab whose file is the lab's solution, which the page shows only after the learner confirms. A lab that declares no tabs gets only the focus tab. A QEMU lab cannot declare tabs: a single-VM lab gets the focus tab on its VM, a multi-VM lab gets the focus tab on its first VM (titled with the VM's name) and a terminal tab `vm-<name>` for each further VM. |
| `docLinks` | Links to official documentation for the lab, from its `resources` in the training's `astrona.yaml`, as `{"name", "url"}`: `https` only, a non-empty name, at most 20; invalid entries are dropped. The page shows them as **Relevant resources** badges at the top of the task; each opens as a `docs` tab of the right panel (see [`GET /runs/{lab}/doclinks/frame`](#get-runslabdoclinksframe)). Always `[]` in an exam, and when the lab lists none. |
| `canSubmit` | `true` when `POST /submit` would grade now: a lab or exam that was started and is still running (an expired exam can still be submitted). Never for a playground. |
| `submit.tracking` | Where a result goes. `account`: the run has an Astrona lab session; the result is sent when astrona is signed in to that site. `local-only`: never sent. `none`: no lab session; the result stays on this machine. |
| `submit.grading` | A submission of this run is being graded now. |
| `submit.checks` | Only while `grading`, once the checks start: each check and validation script in grading order, as `{"name", "state", "startedAt", "seconds"}`. `state` is `waiting`, `running`, `passed` or `failed`; `startedAt` is set once it runs, `seconds` (its duration) once it is done. Never its command or output. Poll `GET /runs/{lab}` for it while `POST /submit` waits. |
| `submit.submitted` | This run has a saved result (`GET /submission`). |
| `submit.blocked` | Why Submit is not possible now, for the page to show next to the disabled button; `""` when it is. |
| `account` | Whose account this run is saved to, shown as one line on the intro page; the same object as on `GET /launches/{id}`. `astrona run` saves it with the run (`runs/<lab>/run.json`). A run without one, such as a lab started before this field or reopened with `astrona ui`, gets `signed-in` (site only) or `local-only` from its saved lab session, and no `account` otherwise. |
| `runAgain` | The command that starts this lab again, as `astrona run` was given it: the catalog ID, the `--git` source, or the config path (`-c`). Never the cluster name, which `astrona run` does not accept. `""` when the source is not known. |
| `solution` | `GET /solution` has a solution guide to show (after **Start**). Always `false` in an exam (a time limit) or a playground. |

Responses never contain the solution document (only `GET /solution` and a
solution docs tab serve it, and never in an exam or a playground), validation
scripts, check commands, expected values, admin credentials or kubeconfigs.

### `GET /runs/{lab}/questions/{id}`

```json
{
  "id": "q1",
  "title": "Pod basics",
  "html": "<h1>Pod basics</h1>\n<p>Create a Pod named <code>web</code> …</p>\n",
  "points": null,
  "target": "pod-basics",
  "hints": []
}
```

`html` is rendered on the server from the task's Markdown (GitHub-flavoured
tables, strikethrough, autolinks and task lists). Raw HTML in the Markdown is
dropped, and links with `javascript:`, `vbscript:`, `file:` or `data:` URLs are
removed (except `data:image/…`, which the CSP's `img-src 'self'` does not load).
Insert it as HTML; the page's CSP blocks inline script anyway.
`target` is the target the question is about. `hints` is always empty for now.

Before Start an exam or learning lab answers `409 not_started` (a playground
never does). A question id other than `q1` gives `404 not_found`.

### `GET /runs/{lab}/tabs/{name}/doc`

```json
{ "name": "notes", "title": "Notes", "html": "<h1>Notes</h1>\n<p>…</p>\n" }
```

Rendered and sanitised like the question. Only for a docs tab with
`document: true`; any other name is `404 not_found`. Errors: `409 not_started`,
`409 run_stopped`.

### `GET /runs/{lab}/doclinks/frame`

```json
{ "frame": "refused", "reason": "its X-Frame-Options header refuses it" }
```

Whether the documentation link `url` (URL-encoded, exactly as listed in the
run's `docLinks`) can be shown in a frame. A cross-origin frame cannot tell
the page that the site refused it, so the server requests the link once and
reads its response headers; the page then frames the site itself, nothing is
proxied. `frame` is:

| Value | Meaning |
| --- | --- |
| `allowed` | No header refuses framing. The page frames the link. |
| `refused` | CSP `frame-ancestors` (anything but `*`) or `X-Frame-Options` (`DENY`, `SAMEORIGIN`) refuses it, or it redirects to another origin, which the page's `frame-src` does not name. `reason` says which. The page shows **This site does not allow embedding** with an **Open in browser** link. |
| `unknown` | The check failed (offline, timeout, more than 5 redirects, a redirect away from `https`). The page tries the frame. |

Only links in the run's `docLinks` are checked; any other `url`, and any url
in an exam, is `404 not_found`. `allowed` and `refused` answers are cached per
URL for an hour; `unknown` is not cached. The check has an 8-second timeout.

### `POST /runs/{lab}/start`

Request: `{"runId": "<runId>"}`

Response: `200` with the run, exactly as `GET /runs/{lab}`, now with
`status: "running"` (or `"expired"`), `startedAt` and, for an exam,
`deadlineAt`.

Starting is idempotent: a second call, from the same tab or another, returns
the run with the deadline the first call set. A `runId` that is not the current
run gives `409 run_changed`; a stopped lab gives `409 run_stopped`. For a
playground Start is accepted and changes nothing.

Only now does the exam clock start (`astrona status` and `astrona submit` see
it). A run that declined the browser keeps the old behaviour: the clock starts
when the lab is ready.

For a run linked to an Astrona lab session, the first Start also tells the site,
after `start.json` is written: `POST {site}/api/cli/lab-sessions/{id}/start`
with `{"at": "<startedAt>"}`, through the outbox like the other clock signals.
It runs in the background and never changes this answer; offline it waits in the
outbox, and a failure is a warning in the server log. The site keeps the first
start. `astrona ui <lab>` for an exam whose clock already started at readiness
sends the same signal with that time. A local-only or untracked run sends
nothing.

### `POST /runs/{lab}/submit`

Request: `{"runId": "<runId>"}` with an `Idempotency-Key: <key>` header (8 to
128 letters, digits, `-` or `_`; the page makes 32 random hex characters per
confirmed submission).

It does what `astrona submit` does, through the same code
(`labops.Submit`), under the lab's lock:

1. The lab's checks run against the environment.
2. The result is saved in this machine's attempt history.
3. It is sent where the run is tracked (`submit.tracking`). A `local-only` run
   is never sent; a run without a lab session stays on this machine.
4. Every recorded result ends the attempt, pass or fail: one that was sent or
   queued, or, for a run without a lab session (`local-only` or `none`), one
   saved here. The environment is then removed: terminals are closed first,
   then the cluster is deleted. `teardown.keepCluster` does not keep it. Only
   a tracked result Astrona refuses, or one that cannot be sent or queued,
   leaves the environment running (`finished` is `false`).

Grading can take minutes; the response waits for it. The work does not depend
on the request: if the connection drops, grading goes on and
`GET /submission` reports it. Repeating the request with the same key and
`runId` waits for, or returns, the same outcome and never grades twice. A
different key while grading runs gives `409 submit_in_progress`. The key is
only remembered by this server process.

If the checks cannot run (`grading_failed`, `not_trusted`, `lab_busy`),
nothing is recorded or sent and the lab keeps running.

Response: `200` with the submission:

```json
{
  "lab": "pod-basics",
  "runId": "3f1c…",
  "result": {
    "pass": true, "earned": 10, "max": 10, "percent": 100, "passPercent": 66,
    "overTime": false, "elapsedSeconds": 754, "attempt": 2, "time": "2026-11-09T21:40:00Z",
    "checks": [
      { "name": "Pod web is running", "pass": true, "points": 6 },
      { "name": "Pod web uses nginx:1.27", "pass": true, "points": 4 }
    ]
  },
  "recorded": true,
  "sent": true,
  "queued": false,
  "finished": true,
  "environmentRemoved": true,
  "messages": ["Sent to your lab page (attempt 2): https://…", "Lab pod-basics deleted."]
}
```

| Field | Meaning |
| --- | --- |
| `result.checks` | Name, pass/fail and points per check. `hint` only for a failed check of a lab that shows hints. Never the check's command, output or expected value. |
| `result.overTime` | A strict exam submitted after its deadline: it cannot pass. |
| `result.elapsedSeconds` | Time spent on the run until this submission: an exam's clock, otherwise from Start (or from when the lab became ready when it was not started in the workspace). Left out when unknown. |
| `recorded` | Saved in this machine's attempt history. |
| `sent` | Recorded on the Astrona lab page. |
| `queued` | Not sent yet: kept in the outbox and sent once Astrona can be reached. |
| `finished` | The attempt is over, pass or fail: the result was sent or queued, or saved here for a run without a lab session. The environment is then removed. |
| `environmentRemoved` | The cluster was deleted after the submission. |
| `messages` | What astrona said while sending and cleaning up, as sanitised plain text (at most 40 lines). |
| `cleanupError` | Present when the result stands but removing the environment failed. |

The latest submission is kept in `runs/<lab>/submission.json` until the lab
is run again.

Errors: `400 invalid_idempotency_key`, `409 run_changed`, `409 run_stopped`,
`409 not_started`, `409 no_grading`, `409 submit_in_progress`, `409 lab_busy`,
`409 not_trusted`, `500`/`502 grading_failed`.

### `GET /runs/{lab}/submission`

```json
{ "state": "grading" }
{ "state": "failed", "error": { "code": "grading_failed", "message": "…" } }
{ "state": "done", "submission": { …as above… } }
```

`grading` while a submission runs, `failed` when the last one this server ran
could not grade, `done` with the saved result. `404 no_submission` when the run
was never submitted. Read-only.

### `POST /runs/{lab}/destroy`

Request: `{"runId": "<runId>"}`. No grading, nothing is submitted.

- A **playground** is deleted as when the CLI ends it: terminals are closed,
  the cluster is deleted, and the playground's clock is stopped on the site
  with a usage report (unless the run is local only). An expired playground
  can still be ended.
- A **lab** is removed as `astrona destroy <lab>` does: teardown scripts run
  when the lab is trusted (skipped with a message otherwise), the cluster is
  deleted, the exam clock is cleared, and the Astrona lab page is told the lab
  was removed. A lab from your own files with `teardown.keepCluster` answers
  `409 environment_kept` and nothing is removed; a catalog or tracked lab is
  always removed.

Response: `200 {"environmentRemoved": true, "messages": ["…"]}`.

Errors: `409 run_changed`, `409 submit_in_progress`, `409 environment_kept`,
`502 destroy_failed`.

### `GET /runs/{lab}/terminals`

```json
{
  "terminals": [
    { "id": "Zk3q…", "tab": "main", "target": "pod-basics", "status": "running",
      "exitCode": null, "createdAt": "2026-11-09T21:01:03Z", "attached": true }
  ]
}
```

`status` is `running` or `ended`; `exitCode` is set once it ended (`-1` when
the process was killed by a signal). Ended terminals stay listed until the tab
opens a new shell, which replaces the ended one, so the page can show "Shell
ended". `POST /terminals/close` ends one tab's; `astrona ui stop` ends them all.

### `POST /runs/{lab}/terminals`

Request: `{"runId": "<runId>", "tab": "main"}` for one of the run's terminal
tabs (`tab` may be omitted for the focus tab), or
`{"runId": "<runId>", "tab": "term-3f9a1c2e", "target": "<target>"}` for a
terminal the learner opened with **+ New terminal**: `tab` is `term-` and 4
to 16 lowercase letters or digits the page picks, and `target` is the `name`
of one of the run's `targets`. A declared tab may also name its own target.
The page cannot name a command or anything else: the new shell is the same
isolated terminal as the focus tab's, on a target of the run. Lab tab names
cannot have the `term-<id>` form.

Each tab has at most one shell. Response: `200` with the tab's terminal when it
is still running (nothing new is started), or `201` with a new one; a new one
replaces the tab's ended shell. While one request starts a tab's shell, another
for the same tab gets `409 terminal_starting`.

On a `kind` target this can take a minute the first time on a machine, while
the terminal image is built. The terminal is a hardened container on the Kind
network that mounts only the lab's own workspace directory; its prompt is
`astrona-cli >` and `$HOME` is `/workspace`. If the container cannot start, the
answer is `502 terminal_failed` and nothing else is started in its place.

On a `vm` target the terminal is an `ssh -tt` login into that VM as the lab's
student user, with the lab's own key and known-hosts file, no SSH config
(`-F /dev/null`), no agent, no forwarding and no escape character, and a
10-second connect timeout. The shell starts in `~/.astrona/runtime/<lab>` in
the VM. The lab's resources are installed in the student's home first, as for
`astrona ssh`. If that fails, the answer is `502 terminal_failed`. If ssh
cannot connect, the terminal ends with ssh's error and exit code `255`. There
is never a fallback to a shell on the host.

Errors: `409 not_started`, `409 run_changed`, `409 run_stopped`,
`409 terminal_starting`, `400 unknown_tab`, `400 unknown_target`,
`429 too_many_terminals` (a lab has at most 12 terminals, running or ended,
across all its tabs), `501 unsupported`.

### `POST /runs/{lab}/terminals/close`

Request: `{"runId": "<runId>", "tab": "term-3f9a1c2e"}`. The learner closed a
terminal tab: its shell ends (a `kind` terminal's container is removed; a VM's
ssh login ends) and it no longer counts towards the 12. Files in the lab are
kept. `tab` is one of the run's terminal tabs or a `term-<id>` tab. Response:
`200 {"closed": 1}`, or `{"closed": 0}` when the tab had no shell. Errors:
`409 run_changed`, `409 terminal_starting` (its shell is still starting),
`400 unknown_tab`.

### `GET /runs/{lab}/solution`

The lab's solution guide (`docs.solution`), rendered and sanitised like a
question: `{"title": "Solution guide", "html": "<h1>…"}`. The page asks the
learner before it shows it. Reading it changes nothing: the attempt is not
marked and nothing is sent. `astrona run` keeps it in the run record only for
a learning lab, so in an exam (a time limit) or a playground the answer is
always `404 no_solution`, as for a lab without one. Errors: `404 no_solution`,
`409 not_started`, `409 run_stopped`.

### `GET /ws/v1/terminals/{id}?ticket=<ticket>`

A WebSocket upgrade. The request must carry the page's own `Origin` (browsers
do). Before the upgrade the server answers with a normal HTTP error:
`401 unauthorized` (missing, used or expired ticket), `404 not_found` (unknown
terminal), `400 bad_request` (not an upgrade).

Frames:

- **Binary frames are terminal bytes**, in both directions. Client to server:
  keyboard input, at most 64 KiB per frame. Server to client: output.
- **Text frames are JSON control messages**, never mixed into the byte stream.

Server to client:

| Message | Meaning |
| --- | --- |
| `{"type":"attached","terminal":{…terminal object…}}` | Always the first frame. Then the buffered output (up to 1 MiB) follows as binary frames, then live output. Clear the screen before writing the replay. |
| `{"type":"ended","exitCode":0}` | The shell exited. The connection closes with code `1000`. |
| `{"type":"takenOver"}` | The same terminal was opened in another tab or view. Closes with `4001`. Do not reconnect automatically. |
| `{"type":"overrun"}` | This view fell more than the buffer behind. Closes with `4002`; reconnect to get a fresh replay. |
| `{"type":"pong"}` | Answer to a ping. |
| `{"type":"error","message":"…"}` | A control frame was invalid or a resize failed, or input was dropped because a strict exam's time is up. The connection stays open. |

Client to server:

| Message | Meaning |
| --- | --- |
| `{"type":"resize","cols":120,"rows":32}` | Set the terminal size. Both 1–1000. Send it right after `attached` and on every resize. |
| `{"type":"ping"}` | Keep-alive; answered with `pong`. |

Other close codes: `1001` when the server shuts down or the terminal is
replaced, `1008` for a control frame that is not valid JSON, `1009` for a frame
larger than 64 KiB.

In a strict exam the terminal is read-only once the deadline has passed: the
server drops keyboard input (and says so once per attachment), while output
still flows. Submit still works.

Opening the same terminal again takes it over: only the newest view can type.
Closing the WebSocket (tab closed, page reloaded) does not end the shell;
reattaching replays its recent output, so the shell and its history survive a
reload.

## Framing

The page is served with the strict policy above plus a `frame-src` that names
exactly the origins of the current runs' `browser` tabs, `docs` URL
tabs and `docLinks` (none in an exam), computed when the page is loaded. It never names the server's own
origin. Entering a run from setup or the lab list automatically reloads the
document, so tabs for labs created after the page opened can be framed.
Frames are sandboxed (`allow-scripts allow-same-origin
allow-forms allow-popups allow-downloads`); nothing is proxied through the
server. A lab's `docs` URL tab whose site refuses to be framed
(`X-Frame-Options` or CSP `frame-ancestors`) stays blank; the tab's **Open in
new window** shows it in a separate window instead. A tab opened from a
`docLinks` badge is checked first with
[`GET /runs/{lab}/doclinks/frame`](#get-runslabdoclinksframe) and shows an
**Open in browser** fallback instead of a blank frame.

## Lifetime

- The server writes `~/.astrona/workspace/server.json` (mode 0600) with its
  PID, port and secret, and removes it when it exits.
- It exits after 15 minutes with no requests, no open WebSocket, no open
  terminal and no running lab it was opened for. `astrona ui stop` stops it at
  once and closes every terminal; labs keep running.
- If the server process ends, its terminals end with it. The lab, its exam
  clock and the files under `/workspace` survive.

## Where state lives

| File | Written by | Contents |
| --- | --- | --- |
| `~/.astrona/workspace/server.json` | server | PID, port, start time, version, per-start secret. |
| `~/.astrona/workspace/server.log` | server | Its log. |
| `~/.astrona/workspace/launches/<id>/launch.json` | `astrona run` | A browser-first run before its lab exists: stage, terms notice, setup progress. |
| `~/.astrona/workspace/launches/<id>/answer.json` | server | The page's answer to the terms. |
| `~/.astrona/workspace/runs/<lab>/run.json` | `astrona run` | What the page shows for that run: title, mode, rules, the account status, the task and prerequisites Markdown, targets, tabs (with the Markdown of docs tabs), and for a learning lab the solution guide. Never the validation block, and never the solution in an exam or a playground. |
| `~/.astrona/workspace/runs/<lab>/start.json` | Start | When Start was clicked, for which `runId`. Removed when the lab is deleted (submit, destroy, reset, expiry). `astrona submit` uses it only while its `runId` is the lab's current run and it is not earlier than the lab became ready. |
| `~/.astrona/workspace/runs/<lab>/submission.json` | Submit | The latest browser submission for that `runId`: the result as `GET /submission` shows it. |

The whole `runs/<lab>/` folder is removed when the lab is started again, from a terminal or the browser, so a new run never inherits an earlier run's record, Start or result.
| `~/.astrona/exams/<lab>.json` | Start (exam labs) | The exam clock, as before. |

Other profiles use their own home (`~/.astrona-dev/…`).
