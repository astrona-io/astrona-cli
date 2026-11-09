# Workspace API

The browser workspace is a small web page served by `astrona` itself, on
`127.0.0.1` only. This page is the contract between that page and the server
(`internal/workspace`). It describes what the server implements today; routes
marked *later* are reserved and answer `404 not_found` until they exist.

The server is started on demand by `astrona run <lab>` (when you answer yes to
"Open this lab in your browser?") and stopped by `astrona ui stop` or after it
has been idle for a while. See [UI Home and Workspace](../guides/ui-home.md).

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
  | 400 | `unknown_target` | `target` is not one of the run's targets. |
  | 401 | `unauthorized` | Missing or invalid token, launch code or ticket. |
  | 403 | `forbidden` | Not a loopback peer, foreign `Origin`, cross-site `Sec-Fetch-Site`. |
  | 404 | `not_found` | Unknown route, lab, question or terminal. |
  | 405 | `method_not_allowed` | Method not allowed (`OPTIONS` included: there is no CORS). |
  | 409 | `run_changed` | `runId` in the request is not the lab's current run. Reload the run. |
  | 409 | `not_started` | Question text or terminals requested before **Start**. |
  | 409 | `run_stopped` | The lab is no longer running (stopped, destroyed or restarted). |
  | 421 | `unexpected_host` | `Host` is not `127.0.0.1:<port>` or `localhost:<port>`. |
  | 429 | `too_many_terminals` | The lab already has 12 open terminals. |
  | 500 | `internal` | Anything else. The message says what failed. |
  | 501 | `unsupported` | Terminals on a platform without a PTY (native Windows). |
  | 502 | `terminal_failed` | The terminal container could not be started. Nothing else was started instead. |
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

## Routes

All paths are under `/api/v1` unless they start with `/ws/`.

| Method and path | Purpose |
| --- | --- |
| `POST /auth/exchange` | Launch code → session token. |
| `POST /auth/ticket` | Single-use WebSocket ticket. |
| `GET /server` | Version, profile, features. |
| `GET /runs` | Labs the workspace was opened for. |
| `GET /runs/{lab}` | One run: identity, mode, status, clock, questions, targets. |
| `GET /runs/{lab}/questions/{id}` | One question rendered to HTML. After **Start** only. |
| `POST /runs/{lab}/start` | The **Start** button. Idempotent. |
| `GET /runs/{lab}/terminals` | Open terminals. |
| `POST /runs/{lab}/terminals` | Open a terminal on a target. After **Start** only. |
| `DELETE /runs/{lab}/terminals/{id}` | Close a terminal. |
| `GET /ws/v1/terminals/{id}?ticket=…` | Attach to a terminal (WebSocket). |
| *later* `PUT /runs/{lab}/progress`, `POST /runs/{lab}/submit`, `GET /runs/{lab}/submission`, `GET /runs/{lab}/events` | Not implemented yet. |

GET routes never change saved state, so polling them is safe.

### `GET /server`

```json
{
  "version": "0.5.0",
  "profile": ".astrona",
  "startedAt": "2026-11-09T21:00:00Z",
  "serverNow": "2026-11-09T21:05:12Z",
  "features": ["runs", "terminals"],
  "assets": "built"
}
```

`assets` is `"placeholder"` when the binary was built without the frontend.

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
  "canSubmit": false
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
| `targets` | Where terminals can run. `name` is what `POST /terminals` takes; `label` is the display name (`main`, or a linked lab's name). `views` lists what the target offers: `["terminal"]` today; a desktop will add `"desktop"`. |
| `canSubmit` | Always `false` until submit lands in the browser. Submit with `astrona submit` meanwhile. |

Responses never contain the solution document, validation scripts, check
commands, expected values, admin credentials or kubeconfigs.

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

### `GET /runs/{lab}/terminals`

```json
{
  "terminals": [
    { "id": "Zk3q…", "target": "pod-basics", "status": "running",
      "exitCode": null, "createdAt": "2026-11-09T21:01:03Z", "attached": true }
  ]
}
```

`status` is `running` or `ended`; `exitCode` is set once it ended (`-1` when
the process was killed by a signal). Ended terminals stay listed until they are
deleted, so the page can show "Shell ended".

### `POST /runs/{lab}/terminals`

Request: `{"runId": "<runId>", "target": "pod-basics"}`. `target` may be
omitted for the main target.

Response: `201` with one terminal object as above.

This can take a minute the first time on a machine, while the terminal image is
built. The terminal is a hardened container on the Kind network that mounts only
the lab's own workspace directory; its prompt is `astrona-cli >` and `$HOME` is
`/workspace`. If the container cannot start, the answer is
`502 terminal_failed` and nothing else is started in its place.

Errors: `409 not_started`, `409 run_changed`, `409 run_stopped`,
`400 unknown_target`, `429 too_many_terminals`, `501 unsupported`.

### `DELETE /runs/{lab}/terminals/{id}`

Ends the shell (hang-up, then kill after three seconds), removes its container,
and forgets the terminal. Response `204`, also for a terminal that had already
ended. Unknown id: `404 not_found`.

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
| `{"type":"error","message":"…"}` | A control frame was invalid or a resize failed. The connection stays open. |

Client to server:

| Message | Meaning |
| --- | --- |
| `{"type":"resize","cols":120,"rows":32}` | Set the terminal size. Both 1–1000. Send it right after `attached` and on every resize. |
| `{"type":"ping"}` | Keep-alive; answered with `pong`. |

Other close codes: `1001` when the server shuts down or the terminal is
deleted, `1008` for a control frame that is not valid JSON, `1009` for a frame
larger than 64 KiB.

Opening the same terminal again takes it over: only the newest view can type.
Closing the WebSocket (tab closed, page reloaded) does not end the shell;
reattaching replays its recent output, so the shell and its history survive a
reload.

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
| `~/.astrona/workspace/runs/<lab>/run.json` | `astrona run` | What the page shows for that run: title, mode, rules, the task and prerequisites Markdown, targets. Never the solution or the validation block. |
| `~/.astrona/workspace/runs/<lab>/start.json` | Start | When Start was clicked, for which `runId`. |
| `~/.astrona/exams/<lab>.json` | Start (exam labs) | The exam clock, as before. |

Other profiles use their own home (`~/.astrona-dev/…`).
