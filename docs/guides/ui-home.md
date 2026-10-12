# UI home and lab workspace

Open the terminal home screen:

```sh
astrona ui
```

Home opens even when no labs are running or the container engine is offline.
It has three sections:

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

Tab changes sections, arrow keys select entries, Page Up/Down scroll details,
`r` refreshes, `?` opens Help, and `q` or Ctrl-C closes Home. The screen refreshes
every five seconds. Closing Home leaves labs running.

Press `n` to enter a catalog lab ID or local path and start a lab through the
normal startup prompts. After startup, select the run and press Enter to open
its workspace. Press `p` to edit Preferences, even with no active runs. Boolean
settings use `yes` / `no`; blank input keeps the current value.

Home and the workspace use bordered panels, highlighted selections and fixed
keyboard hints. Errors appear in an alert panel: F1 opens the full message,
Page Up/Down scrolls it, and Esc returns. Save confirmations use status panels.

## Work on a lab

Start a new lab and enter its workspace:

```sh
astrona run ./labs/my-lab --ui
```

Or open an existing lab directly:

```sh
astrona ui astro-my-lab
```

Development builds use `astrodev-` names and separate local data. Use the name
displayed by Home or `astrona list`.

Within the workspace:

- Arrow keys select a cluster. `s` or Enter opens a shell with that lab's
  isolated kubeconfigs; exiting the shell returns to the workspace.
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
