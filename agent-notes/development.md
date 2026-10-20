# Development reference

## Conventions

- Errors: wrap with context using `%w`, return early, let Cobra turn errors into a non-zero exit.
- Cleanup: functions that create temp resources (e.g. `DownloadToTemp`) return a `func()` cleanup alongside the value/error — call it with `defer` at the call site.
- Output: user-facing progress messages go to stdout via `fmt.Printf`; no external logger. Warnings/notices via `ui.Warnf`/`ui.Infof` (stderr, colored only on a terminal).
- Prompts: a confirmation takes `--yes`/`-y`; approving someone else's code (remote lab, another astrona binary) takes its own explicit flag (`--trust`, `--install-version`), never `-y`. Without a terminal, never block — fail and name the flag.
- Exit codes: `notPassed(...)` (exit 2) for "graded, didn't pass"; any other error exits 1.
- Naming: exported Go-style `PascalCase` for functions and types reused across packages.
- Grading: any code path that decides whether a lab passes must go through `Proctor.Grade` (`internal/proctor/proctor.go`).
- Docs: any change that adds/renames a flag, command, config field, or alters user-facing behavior must update `docs/` (mkdocs site — `docs/reference/cli/` for commands, `docs/reference/lab-config.md` for config fields, `docs/concepts/` for behavior/architecture changes, `docs/guides/` for workflow changes) in the same change, not as a follow-up.

## Build & run

The docs theme follows the public frontend's Mission Control tokens. Styling is
in `docs/stylesheets/extra.css`, templates in `docs/overrides/`, and optional
motion controls in `docs/javascripts/motion.js`. Keep Material's built-in search,
navigation, code copying and mike version selection. Theme changes require a
strict docs build and desktop/mobile browser checks; see `docs/contributing.md`.
Never make documentation depend on animation or JavaScript to be readable.

```sh
just build # creates ./astrona only; development data goes to ~/.astrona-dev
./astrona --help
go vet ./...
go test ./...
```

Plain `go build -o astrona ./cmd/astrona` also produces a development build.
Release packaging sets `-X main.Version=vX.Y.Z` and uses release state. There is
no Justfile install recipe; never move local test binaries onto the user's PATH.
