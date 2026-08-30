# Run Logs

Every lifecycle command — `astrona run`, `test`, `submit`, `destroy` — tees its **full** output to a per-run log file, no matter which display mode you used. The compact step view (or `--verbose` stream) is what you see live; the log file is the complete record, including every line of subprocess output the step view collapses.

## Where they live

```
~/.astrona/logs/<command>-<lab>-<UTC timestamp>.log
```

e.g. `run-astro-disk-lab-20260830T120000Z.log`. The directory is created on first use and never pruned automatically — see [`astrona logs clean`](../reference/cli/astrona_logs_clean.md).

## File format

```
# astrona run  lab=astro-disk-lab  2026-08-30T12:00:00Z
# astrona run -c ./labs/disk --verbose

=== Lab: astro-disk-lab ===

--- create cluster ---
<raw docker / kind / qemu / kubectl / bash output>
--- ok: create cluster (42s) ---

--- apply manifests ---
--- skip: apply manifests (none) ---

# done: result=ok steps=2/0/0 duration=44s
```

- **Line 1** — command, lab name, and start time (RFC 3339, UTC). Authoritative; the filename is only a fallback.
- **Line 2** — the exact argv the run was invoked with.
- `=== … ===` — a section header (a lifecycle phase, or a per-VM group).
- `--- … ---` — a step starting; `--- ok:` / `--- skip:` / `--- FAIL:` — that step resolving, with elapsed time or a skip reason.
- `[WARN] …` — a non-fatal warning.
- **Footer** — `# done: result=<ok|fail> steps=<ok>/<skipped>/<failed> duration=<elapsed>`, written when the command finishes (including a clean interrupt). A log with no footer is from a run that was killed outright; `astrona logs list` shows its result as `UNKNOWN`, or `FAIL` if any step had already failed.

## Reading them back

[`astrona logs`](../reference/cli/astrona_logs.md) (alias `log`) is the read side:

| Command | Purpose |
| --- | --- |
| [`astrona logs list`](../reference/cli/astrona_logs_list.md) | Table of recorded runs, newest first — index, command, lab, start, duration, result, size. |
| [`astrona logs view [ref]`](../reference/cli/astrona_logs_view.md) | Show one run's log through your pager (`$PAGER`, else `less`); `--tail`, `--grep`, `--no-pager`. |
| [`astrona logs path [ref]`](../reference/cli/astrona_logs_path.md) | Print a log's absolute path — for `cat`, `grep`, editor, etc. |
| [`astrona logs export [ref]`](../reference/cli/astrona_logs_export.md) | Copy logs out of `~/.astrona` to a file or directory. |
| [`astrona logs clean`](../reference/cli/astrona_logs_clean.md) | Delete old logs (`--keep`, `--older-than`; requires `--yes` to actually delete). |

### Referring to a run

Anywhere a `[ref]` is accepted:

- **omitted** or `latest` — the most recent run (honouring `--lab` / `--command`).
- an **index** from `astrona logs list` — `1` is always the newest.
- the **UTC stamp** from the filename — `20260830T120000Z`.
- a **filename** or an absolute **path** to the log.

### Output formats

`logs list`, `logs view`, and `logs export` take `--format`:

- `raw` (view/export) / `table` (list) — the default human view.
- `json`, `yaml` — machine-readable. For `list` it's the array of run metadata; for `view` / `export` it's the parsed log: `{meta, lines: [{kind, text}, …]}` where `kind` is `header`, `section`, `step`, `ok`, `skip`, `fail`, `warn`, `done`, or `output`.
