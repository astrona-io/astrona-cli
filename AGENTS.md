# Astrona CLI contributor instructions

Astrona manages local Kind and QEMU labs on the user's real machine. Prefer small,
idiomatic Go changes with clear errors and actionable CLI output.

## Always apply

- Preserve user work, private notes, credentials, and unrelated running labs.
- Keep development/release profiles isolated through `internal/apphome`.
- Student Kind terminals may only write their per-run workspace. Never mount host
  homes, engine sockets, or SSH agents. No fallback to a host shell.
- Resource files belong to the lab; opening a terminal must preserve edits and
  must never run a resource automatically. Host resource execution is explicit.
- Polling is read-only. Destructive actions must retain their existing approval
  and run-identity checks. Settings review is optional; terms and code trust are separate.
- Keep JSON stdout clean; prompts and progress belong on stderr in JSON mode.
- Update `docs/` with user-visible behavior, command, flag, or schema changes.
- Run `just check` before committing. Run `just build-docs` after docs/help changes.
  `just build` creates `./astrona`; never move test binaries onto the user's PATH.

## Read only the references relevant to the task

- [Architecture and package responsibilities](agent-notes/architecture.md): when
  navigating command flows, runtime provisioning, UI, settings, and local data.
- [Security boundaries](agent-notes/security.md): before changing filesystem access,
  resource copying, remote code, command execution, SSH, or downloads.
- [Development conventions and checks](agent-notes/development.md): implementation,
  CLI prompts/exit codes, grading, docs, build and validation details.

These references retain the detailed guidance formerly in `CLAUDE.md`; avoid
loading all of them for a small, unrelated task. Use relevant installed skills
when they improve the work; no additional local skill is required just to read
this repository.
