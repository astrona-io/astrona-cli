# Usage data (telemetry)

The `astrona` CLI sends usage data only to improve the Astrona platform and its
labs: which labs are used, how long they take, and where people get stuck or
hit errors. It is on by default and you can turn it off at any time. The
[privacy policy](https://astrona.io/privacy#cli-usage-data) covers it in full.

## What is sent

Every batch carries a **random install id**: a UUID made on your computer the
first time an event is recorded, kept in `~/.astrona/telemetry/install-id`. It
is not derived from your hostname, network card or user, and it stays the same
when you sign in or out. When you are signed in, events are also linked to your
account (the site checks your sign-in; the CLI never sends an account id itself).

Each batch also says the CLI version, the operating system (`linux`, `darwin`,
`windows`) and the CPU architecture (`amd64`, `arm64`). The events are:

| Event | What it records |
| --- | --- |
| `run_started` | catalog lab name (empty for a lab from a local folder or URL), lab or playground, Kind or VM, container engine and its major version |
| `run_finished` | ready, cancelled or failed; seconds to provision and to become ready; an error category |
| `timer_started` | you clicked Start in the browser workspace |
| `submit` | attempt number, pass or fail, checks passed of total |
| `teardown` | ok or failed; how long the teardown and the whole session took |
| `run_abandoned` | a lab removed without ever being submitted |
| `tab_opened`, `tab_closed` | the workspace tab type: terminal, docs, solution, or doc link (its position in the lab's list, never the URL) |
| `update_shown` | the "new astrona release" notice was shown |

Error categories are a fixed list: `engine_unreachable`,
`insufficient_resources`, `image_pull`, `timeout`, `readiness`, `provision`,
`port_conflict`, `interrupted`, `network`, `grading`, `other`. The CLI reads the
error message on your computer to pick one; the message itself is never sent.

**Never sent:** your files, file paths, commands, command lines, environment
variables, error messages, host or user names, or your IP address. The site
sees your IP address when the CLI connects and uses it only to limit request
rates; it is not stored or passed on.

`astrona telemetry status` prints this list, the install id and how many
events are waiting.

## Turning it off

Any of these stops it:

```bash
astrona telemetry off       # saved in ~/.astrona/settings.json
export ASTRONA_TELEMETRY=0  # this shell or CI job
export DO_NOT_TRACK=1       # the cross-tool convention
```

`astrona telemetry off` also deletes the events not yet sent.
`astrona telemetry on` turns it back on, and `astrona telemetry reset` replaces
the install id with a new random one.

Runs with `--local-only` or `--headless` record no run events, and the CLI
never records anything in its own test binaries.

## How it is sent

Events are written to `~/.astrona/telemetry/spool.jsonl` (at most 500, none
older than 29 days) and sent in batches of up to 100 to
`https://astrona.io/api/cli/events`: at the end of a command within two
seconds, otherwise by the same background sender that delivers lab results
saved while offline. A failed send never fails or slows a lab; after a failure
the CLI waits 15 minutes before trying again at the end of a command. Events
recorded while signed in are sent with your sign-in; when that sign-in is gone
they are sent as a guest with only the install id.

## Deletion and retention

Astrona keeps the events for about 13 months. Deleting your Astrona account
deletes the events linked to it and every event of each install id that was
ever used while signed in to it.
