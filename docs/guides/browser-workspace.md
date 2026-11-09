# Browser workspace

The browser workspace is a local web page for working on a Kind lab: the task
on one side, terminals into the lab on the other. `astrona` serves it itself,
on `127.0.0.1` only.

## Open a lab in the browser

Once the trust prompt (if any) is settled, an interactive `astrona run` of a
Kind lab asks:

```text
Open this lab in your browser? (yes/no/always/never):
```

- `yes` sets the lab up as usual, then starts the local workspace server (or
  reuses a running one), prints a link and opens it in your default browser.
- `no` keeps the command-line flow.
- `always` and `never` answer the same way and save the answer as the
  [`browser` setting](settings.md), so later runs do not ask.

Answer in advance with a flag:

```sh
astrona run ./labs/my-lab --browser      # open it without asking
astrona run ./labs/my-lab --no-browser   # never open it, never ask
```

The question is only asked when stdin and stdout are a terminal; otherwise the
answer is no (unless you pass `--browser`). On a machine without a browser
(no display) nothing is asked; with `--browser` the link is printed and nothing
is opened. `--headless` never opens the browser.

The link is printed on every yes, so you can open it yourself:

```text
Browser workspace: http://127.0.0.1:47300/#code=…&lab=my-lab
```

The `#code=` part is a single-use launch code that expires after two minutes.
It stays in the fragment, so it never reaches the server log or a referrer.
For an exam, the timer does not start when the lab is ready: the page shows
the time limit and rules, and the timer starts when you click **Start**.

## Reopen a running lab

`astrona ui <lab>` opens a running lab again, for a link that expired or a tab
you closed:

```sh
astrona ui my-lab
astrona ui            # the only running lab; with several, it lists them
```

It starts the workspace server if needed, prints a new link with a fresh code
and opens it (or prints it when there is no browser). It never restarts the lab
or its timer: an exam whose clock already runs keeps it, even when the lab was
started without the browser. It reads the saved local config and cached
repository; it does not check GitHub, update the checkout or rerun setup.

A lab named `status`, `stop` or `serve` is opened by its full name, since the
short name is a subcommand: `astrona ui astro-status` (`astrodev-status` for
development builds). `astrona list` shows the full names.

## QEMU labs

QEMU (VM) labs do not open in the browser yet, and `--browser` on a QEMU lab is
an error. Once a QEMU lab is ready, `astrona run` prints the `astrona ssh`
command for each VM; `astrona status <lab>` shows them again.

## The workspace server

The workspace server listens on `127.0.0.1` only. Its terminals are the same
isolated containers as `astrona shell`: they may only write the lab's own
workspace, and never see your home directory, container engine socket or
SSH agent. See [terminal workspaces](../concepts/runtimes.md#terminal-workspaces)
for file access and the protection boundary. The server stops by itself after
15 minutes without a page, terminal or running lab.

```sh
astrona ui status            # running?, a fresh link, pages and terminals
astrona ui status -o json
astrona ui stop              # stop the server and its terminals; labs keep running
```

`astrona ui status` prints a new link with a fresh code each time.
`astrona ui stop` ends every browser terminal but leaves the labs running;
`astrona run` or `astrona ui <lab>` starts the server again. The page's HTTP API
is described in the [workspace API reference](../reference/workspace-api.md).

## Guest login reminder

When you are not logged in, each lab run asks before starting the lab:

```text
Log in now? (yes = log in, no = continue as guest, never = continue as guest and stop asking):
```

`yes` opens the browser sign-in flow and continues startup when it completes.
`never` saves `login-reminder=no`. Catalog labs can track account progress after
login; local labs stay local. Restore reminders with
`astrona settings set login-reminder yes`. Noninteractive catalog runs still
require `--without-login` when signed out.

For automation, use [headless mode](ci-integration.md#headless-setup-for-external-checks):
`astrona run <lab> --headless` skips the browser question and login reminder and
returns after setup.

## History

Submission history uses the local results. Completed runs are recorded under
`~/.astrona/results/runs` (`~/.astrona-dev/results/runs` for development
builds). These records contain names, catalog identifiers and timestamps,
without credentials or session IDs, and survive destruction and cache cleanup.
`astrona progress` summarizes graded attempts.
