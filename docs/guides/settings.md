# Persistent CLI settings

`astrona settings` lists your preferences and shows whether each value is saved
or comes from the default. Preferences live in `~/.astrona/settings.json`,
separate from credentials, lab state, and remote-code approvals.

```sh
astrona settings
astrona settings view
astrona settings view -o json
astrona settings review
astrona settings set keep-context yes
astrona settings set parallel 2
astrona settings set cache-retention-days 30
astrona settings set cache-max-size-gb 50
astrona settings get keep-context
astrona settings unset keep-context
astrona settings path
astrona settings -o json
```

| Setting | Default | Effect |
|---|---|---|
| `verbose` | `no` | Stream full command output instead of the compact step view |
| `keep-context` | `no` | Leave kubectl's current-context unchanged on commands with `--keep-context` |
| `parallel` | `1` | Start up to 1–5 linked clusters at once on commands with `--parallel` |
| `ui-task-width` | `30` | Default task pane width as a percentage of the lab view (20–60) |
| `login-reminder` | `yes` | Offer guests login before each lab run; `no` hides reminders |
| `update-check` | `yes` | Check for new CLI releases and show update notices |
| `cache-retention-days` | `30` | Maximum disposable-cache age; whole days from 1 to 36500 |
| `cache-max-size-gb` | `50` | Combined disposable-cache limit in decimal GB; `0` means unlimited size |

`settings`, `settings get` and `settings view` display booleans as `yes` / `no`.
JSON output and saved files retain `true` / `false`.

Explicit flags override saved values, including `--verbose=false`,
`--keep-context=false`, and `--parallel=1`. `--update-check=false` disables
update checks for one command; save it with
`astrona settings set update-check no` to keep that preference. The existing
`ASTRONA_NO_UPDATE_CHECK` environment variable, CI detection, and non-terminal
output still suppress update checks even when the setting is yes.

Only saved choices are written to the JSON file. Review saves each chosen value,
including a default you choose to keep, so later releases cannot silently replace
that choice. `unset` removes one override
without changing the others; omitted keys use the built-in defaults. Listing
or reading settings does not create a file. The JSON output contains configured
values plus defaults, before flags or environment-based suppression are applied.

Writes are atomic, with file permissions `0600` and a `0700` directory when
created. A lock prevents simultaneous CLI edits from overwriting each other;
retry if another command is changing settings. Settings commands do not contact
the network, fetch a lab, or change your sign-in.

You can also edit the JSON file directly. Values must be JSON booleans or whole
numbers for `parallel` and the cache settings. `parallel` accepts 1 to 5;
`cache-max-size-gb` accepts 0 to 9223372036 without overflowing the byte count.
Unknown keys and invalid values are rejected
before a normal command runs. A damaged file is never silently overwritten:
use `astrona settings path` to find and repair it. Settings and lock files must
not be symlinks. Remote-code trust and automatic confirmation cannot be enabled
through settings.

## Change settings inside the lab workspace

In `astrona ui` or a lab started with `--ui`, press `p` to open Preferences.
With no running labs, `astrona ui` opens a home screen. Press `n` or Enter to
start a Kind lab by catalog ID or local path, `p` for Preferences, or `r` to
find labs started from another terminal. Select a running lab and press Enter
to open it. Press `q` to close the home screen. Starting a lab shows one Yes/No
notice covering terms, settings, lab code and downloads. Tab opens the details.
Yes starts setup inside the UI; No cancels with a message. Downloads, progress
and errors stay in the terminal pane. Cancelling or failing returns to Home.
After startup, select the run and press Enter to open its workspace.
Inside the workspace, Enter shows the task; `s` opens a shell inside the UI.
Type `exit` in the shell to return to the workspace. Ctrl+] opens pane controls
for scrolling recent output or closing the UI.

Starting an existing lab asks whether to destroy its current work and start over.
No keeps it running. Yes discards it without saving or submitting, then starts a
local-only run: no account session, progress or results are sent to the Astrona
API. To reopen a lab without restarting, use `astrona ui`. Both UI commands work
from inside a lab shell. See [the UI guide](ui-home.md) for details.

The workspace keeps its header and keyboard hints visible around bordered panels.
Starting a lab, editing a preference and confirming destruction use dialogs.
Errors appear in an alert panel; press F1 to read the full message, Page Up/Down
to scroll it, and Esc to return. Save confirmations appear as status messages.

Use the arrow keys to select a setting. The editor shows its current value,
whether it is saved or a default, its built-in default and an explanation.
Press Enter, type a value, then Enter to review the current and proposed values,
why the change is being proposed, its effect and when it applies. Changes you
request here are optional. Press `y` to approve or `n` to decline and keep the
current value; Enter returns to editing without saving. Esc cancels
the edit without saving; another Esc returns to the lab. Page Up/Down scrolls
long descriptions. Press `r` in the list to reload choices saved by another command.

The editor uses the same file as `astrona settings path`. It validates values
before saving and refuses to overwrite a setting changed elsewhere while you
were editing it. Boolean settings use `yes` / `no`. A blank answer keeps the
current value, including the default `yes` for `update-check` when no value is
saved. Editing preferences does not approve new settings definitions or usage terms.

Saved options apply to future CLI commands, not the current workspace's options
or running lab. Cache policy is checked after the workspace closes; saving a
cache preference does not itself delete files. Opening the UI remains opt-in
through `--ui` or `astrona ui`, not a persistent setting.

## Review settings after an upgrade

`astrona settings view` gives each setting a readable block with its current
value, source, built-in default and explanation. Booleans appear as yes/no;
cache settings use days or GB when supported by your CLI version. It also lists
pending changes and a review ID. `-o json` includes the same information;
the existing `astrona settings -o json` remains a flat values object. Both are
read-only and show preferences before flags or environment overrides.

On first use of the review feature, Astrona asks about every setting. Later it
asks only when a setting is added, removed, or changes its default, explanation
or behavior revision. A CLI version change alone does not trigger another review.
The review happens after terms acceptance but before lab loading or update checks.
You can also start it with `astrona settings review`.

For each pending setting, the prompt shows its default and current choice.
It explains why review is needed: a new setting, a changed default, explanation,
behavior revision or explicit-choice requirement. A new default does not replace
a saved value. Each chosen value is shown as current → proposed before the final
approval prompt. Declining leaves both preferences and the review receipt unchanged;
commands that require review stay blocked until it is approved.
Enter a value, press Enter to keep the current choice, or type `q` to cancel.
Boolean settings use `yes` / `no`; `y` / `n` and `true` / `false` also work.
`update-check` defaults to `yes` when no value is saved; a saved `no` stays selected.
Its explanation states that release checks contact GitHub.
Invalid answers show an error and ask again, keeping your earlier answers.
A final `[y/N]` confirms the review. Nothing is saved before that confirmation;
`q` or EOF cancels without saving.
Saved overrides stay selected unless you change them. Reviewed settings that
have been removed are deleted from preferences only after confirmation; unknown
keys not found in the previous review still require manual repair.

The receipt lives in `~/.astrona/settings-review.json`, separate from preferences
and usage-term acceptance. It records the reviewed definitions and CLI version,
not a server identity or tracking token. Writes share the settings lock and use
private atomic files. If interrupted between saving choices and the receipt,
the next command asks again. `data reset --yes` clears both files. Unsetting
`update-check` restores its default `yes` without requiring another review.

Help, completion, settings, local data maintenance, terms display, internal
background helpers, stop/destroy and logout remain available without review.
Older binaries do not enforce this gate. No new telemetry or account tracking
is enabled by this feature; existing signed-in lab progress tracking is separate.

Without an interactive terminal (including CI), a pending review stops the
command instead of assuming agreement. `--yes`, `--trust` and `--accept-terms`
do not approve settings. Set any overrides, inspect the view, then acknowledge
its exact ID:

```sh
astrona settings set update-check no
astrona settings view
astrona settings review --accept <review-id-you-inspected>
```

The ID binds to definitions and saved choices, so it changes when either changes.
Do not pipe a newly generated ID straight into `--accept`; keep the reviewed ID
in CI configuration and update it after inspecting changes. For a fresh runner,
use the ID displayed with that runner's exact saved settings, before review
pins additional defaults. An existing current receipt permits headless commands;
it does not remove the separate per-command CI requirement for `--accept-terms`.

Cache limits are local preferences, not lab flags, and are not forwarded during
version handover. Older CLI releases that validate settings may reject these new
keys; remove the overrides or upgrade that binary before using it.
