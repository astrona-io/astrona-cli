# UI home and lab workspace

Open the terminal home screen:

```sh
astrona ui
```

Home opens even when no labs are running or the container engine is offline.
The header uses a k9s-style layout: labeled status fields on the left,
keyboard shortcuts in columns, and an Astrona wordmark on wide terminals.
It stays visible during setup, grading and shell sessions. The status fields
show your login, account name, site, CLI version and active profile
(Development or Release). Shortcuts change for the workspace, shell and pane
controls. Small terminals use a compact header to leave room for content.

![Astrona Home with sample labs, status fields and shortcut columns](../assets/ui-home.png)

A green **Logged in** means saved credentials have
an unexpired access token. Red **Not logged in** means there is no saved login
for the selected site. Expired credentials show **Login needs checking** in
amber; use `astrona whoami` to check and renew them, or `astrona login` to sign
in. An unreadable credentials file shows **Login unavailable**.

The header rereads local credentials every five seconds. It does not contact
the API or verify whether the server has revoked a login.

Home has three sections:

- **Active runs:** saved runs, their container status and elapsed time. Select
  a running Kind lab or playground and press Enter to open its workspace.
  Stopped, missing or unavailable runs remain visible but cannot be opened.
- **History:** graded submission attempts, scores and completed local runs,
  including playgrounds. Select an entry and press Enter for details; Esc
  returns to the list. The latest UI action also appears here, including its
  result or error. The most recent 100 persistent entries are displayed;
  `astrona progress` summarizes all graded attempts.
- **Help:** controls, starting a lab, the current local profile and any errors
  encountered while reading run information.

The tabs below the content highlight the current section. Press `1` for Active runs,
`2` for History, `3` for Help, or `p` for Preferences. The summary below it
shows running and saved run counts, recent history count and refresh interval.
Tab also changes sections, arrow keys select entries, Page Up/Down scroll details,
`r` refreshes, `?` opens Help, and `q` or Ctrl-C closes Home. The screen refreshes
every five seconds. Closing Home leaves labs running.

Press `n` to enter a catalog lab ID or local path and review the startup
notice. After startup, select the run and press Enter to open
its workspace. Press `p` to edit Preferences, even with no active runs. Boolean
settings use `yes` / `no`; blank input keeps the current value.

Home shows a full-width table with Lab, Type, Status and Active time columns. Narrow
terminals retain Lab and Status. Long names are truncated within their column
so the status remains visible. The current row has a contrasting background.
The content has a single border, with centered titles and section tabs below.
Errors appear in an alert panel: F1 opens the full message,
Page Up/Down scrolls it, and Esc returns. Save confirmations use status panels.

## Time spent and idle pauses

Opening a lab workspace starts a local time spent counter. Keyboard input in
the workspace or its embedded shell counts as activity. After five minutes
without input, an idle dialog pauses the counter and excludes the idle interval
back to your last input. Press Enter on **Continue** to resume; that keypress is
consumed by the dialog. Output, polling and background commands do not count
as activity. The lab and commands keep running while the counter is paused.

The footer shows active time and whether it is paused. Home's Active column
and finished-run history retain the recorded time. Checkpoints are saved every
five seconds and on pause, resume and exit. Time with the UI closed is not
counted, and overlapping UI sessions are counted once. Older runs show a dash
until tracked; activity before opening the workspace is not reconstructed.

This tracks work in the terminal, so reading outside it will eventually pause
the counter. It does not extend exam limits or playground expiry deadlines,
and it does not send activity data to the API. Closing the UI abruptly may lose
up to five seconds since the last checkpoint.

## Work on a lab

Start a new lab and enter its workspace:

```sh
astrona run ./labs/my-lab --ui
```

Startup shows one Yes/No box before downloads or lab changes. Yes accepts the
displayed settings and CLI terms, authorizes the lab's code and required
downloads for this invocation, and continues with setup. Saved preferences
are kept. Catalog labs use your account when signed in; otherwise they run
without account progress tracking. Other labs stay running.

The UI stays open after Yes, with the dashboard behind a setup dialog.
Repository downloads, setup progress, later confirmations, grading and teardown
stay inside the dialog. A spinner and moving bar indicate work in progress;
the bar does not claim a percentage when the command cannot report one. Startup
errors stay visible until you press Enter or Esc to close the UI. The command's
exit code is preserved.

Press Ctrl+] for pane controls. Arrow keys and Page Up/Down scroll recent
output; End returns to the latest output, and Esc resumes input to the lab.
From pane controls, `q` asks before closing the UI and stopping its active
command. Any lab already created remains on your machine. Closing the UI does
not submit results or destroy the lab.

Use Tab to read the terms and setting explanations, arrow keys or Page Up/Down
to scroll, and left/right to select Yes or No. Enter confirms the selection;
`y` accepts directly. No, `n`, Esc or Ctrl-C cancels startup and prints a message.
Cancellation exits with code 3; when starting from Home, it returns there with
a cancellation notice. No leaves existing labs as they are.

When the requested lab already exists, the box asks whether to destroy it
and start over. Yes discards work in that lab and its linked clusters without
saving or submitting it. The old account session is detached locally and its
playground watchdog is stopped without reporting to Astrona. The replacement
run stays local, even when signed in: it creates no account session and sends
no progress or submission results to the Astrona API. Grading still works
locally. Other labs stay running. The CLI skips teardown scripts during this
discard operation and blocks automatic version handover for the local run.

Saved runs are checked before the first notice. If an existing lab is only
found after its config is loaded, a restart confirmation appears then, before
any account access or deletion. A lab replaced by another command while the
dialog is open must be reviewed again. `--yes --ui` does not bypass this check.

You can open the UI from an Astrona lab shell. The inherited shell marker does
not choose which lab to replace; the command's lab argument or selected config
does. Startup leaves that shell's Kubernetes context unchanged. To continue
working on an existing lab without restarting, use `astrona ui <lab-name>`.

The startup notice cannot approve a remote config this CLI cannot parse.
Version handover still requires trust for that exact source or an explicit
`--trust` supplied after reviewing it.

Or open an existing lab directly:

```sh
astrona ui astro-my-lab
```

Development builds use `astrodev-` names and separate local data. Use the name
displayed by Home or `astrona list`.

Within the workspace:

- Arrow keys select a cluster. Enter shows the task and keeps the UI open.
  `s` opens a shell inside the UI, with that lab's isolated kubeconfigs.
  Commands, editors and their output stay in the terminal pane. Ctrl+C
  interrupts the shell's active command; type `exit` to return to the workspace.
- Tab changes between the task and results; Page Up/Down scrolls.
- `p` opens saved preferences. Select a setting, press Enter to edit and
  review, then `y` to save; Esc cancels. Settings apply to future commands,
  and cache policy is checked after the complete UI session closes.
- `g` submits a graded lab through the Proctor and returns to Home. The lab
  stays running after either PASS or FAIL, so you can reopen it or destroy it.
  Playground workspaces do not offer submission.
- `q` or Ctrl-C opens the exit menu. Keep the run active and return Home, submit
  a graded lab, or choose destruction and confirm with `y`. Enter cancels the
  destruction confirmation.

After a completed submission or successful destruction, Home shows the result
and refreshed runs/history. Errors before grading completes, failed
destruction, and `teardown.keepCluster` keep you in the workspace with an
explanation. Runs restarted elsewhere are blocked from workspace actions.

## History and availability

Submission history uses the existing local results. Newly completed runs are
recorded under `~/.astrona/results/runs` (`~/.astrona-dev/results/runs` for
development builds). These records contain names, catalog identifiers and
timestamps, without credentials or session IDs. They survive destruction and
cache cleanup. Older destroyed runs without a record cannot be reconstructed.

Home and the workspace require an interactive terminal outside CI. Workspace
actions currently support Kind; QEMU runs may appear as saved runs, but use
the regular CLI and `astrona ssh` to work on them.

Standalone `astrona submit` and `astrona destroy` keep their normal terminal
behavior. They do not automatically open Home. Closing a terminal never
submits or destroys a lab; existing lab/playground time limits still apply.
