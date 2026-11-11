# Development reference

## Conventions

- Errors: wrap with context using `%w`, return early, let Cobra turn errors into a non-zero exit.
- Cleanup: functions that create temp resources (e.g. `DownloadToTemp`) return a `func()` cleanup alongside the value/error — call it with `defer` at the call site.
- Output: user-facing progress messages go to stdout via `fmt.Printf`; no external logger. Warnings/notices via `ui.Warnf`/`ui.Infof` (stderr, colored only on a terminal).
- Prompts: a confirmation takes `--yes`/`-y`; approving someone else's code (remote lab, another astrona binary) takes its own explicit flag (`--trust`, `--install-version`), never `-y`. Without a terminal, never block — fail and name the flag.
- Exit codes: `notPassed(...)` (exit 2) for "graded, didn't pass"; any other error exits 1.
- Naming: exported Go-style `PascalCase` for functions and types reused across packages.
- Grading: any code path that decides whether a lab passes goes through the proctor (`internal/proctor/proctor.go`). Library code (`internal/labops`, `internal/authoring`, `internal/preflight`) calls `Proctor.Evaluate` and renders with `Report(w, ...)` on the writer it was given; `Proctor.Grade` (Evaluate, then Report on stdout) is only the cmd convenience. forbidigo enforces this, and that those packages never write to stdout/stderr themselves.
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

## Browser workspace frontend

The page served by the browser workspace lives in `internal/workspace/web`
(Vite + Vue 3 + TypeScript, xterm.js for the terminal). It builds into
`internal/workspace/web/dist`, which `go:embed` puts into the binary.

- Node: the version in `internal/workspace/web/.nvmrc`. `just build` runs
  `just web` (`npm ci` + `npm run build`) before `go build`, then restores the
  committed placeholder page so git stays clean. `just web` alone leaves the
  built page in `dist/`.
- Go never needs Node: `dist/` holds a committed placeholder `index.html`, so
  `go build` and `go test` work on a fresh checkout; that binary serves a page
  saying it was built without the workspace assets. Only the placeholder is tracked; never commit build output.
  Releases and Linux packages run `.github/scripts/build-workspace-web.sh`, and
  `check-workspace-web.sh` fails the build if the placeholder would ship.
- `just web-dev` (`npm run dev`) serves the page with a mock workspace server
  from `dev/mock.ts` at `http://127.0.0.1:5173/#code=dev&lab=demo`.
  `MOCK_KIND=playground` mocks a playground; code `expired` shows the 401 page.
  `ASTRONA_WORKSPACE=http://127.0.0.1:<port>` proxies to a real workspace
  server instead. `dev/` is never part of `dist/`.
- `npm run build && npm run preview` serves the built page with the mock under
  the exact Content Security Policy from `internal/workspace/guard.go`. Use it
  to check a change in the browser console before release.
- CSP is `default-src 'self'`: no inline `<script>`, no `style` attributes or
  inline `<style>` in our templates (use classes), no `data:` URIs, no CDN and
  no remote fonts. Fonts are self-hosted from `@fontsource`. While a terminal is
  mounted, `document.createElement('style')` returns an inert element whose text
  is mirrored into constructed stylesheets (`src/terminal/cspStyles.ts`), so
  xterm's runtime `<style>` rules apply without a CSP violation. The terminal
  draws with `@xterm/addon-webgl` (truecolor without `style` attributes); the
  DOM renderer is only the fallback. Check for violations with a
  `ReportingObserver` (`buffered: true`): the console does not always show them.
- The HTTP and WebSocket contract is `docs/reference/workspace-api.md`. All
  calls go through `src/api.ts`; components never call `fetch` directly.
  Question HTML from the server is rendered as HTML, never evaluated.
- The right-hand panel shows the lab's tabs (`src/components/LabTabs.vue`);
  each tab kind has a view (`src/views/types.ts`). A new kind (for example a
  desktop) is a new view component and tab kind, not a change to the workspace
  page. The run gives the tabs the page opens with; the learner opens more
  from the + menu (term-<id> terminals on a run target, task, prerequisites,
  solution guide) and closes them. Open tabs persist per run in localStorage.
- Runtime dependencies must use a licence on the allowlist in
  `build/notices.ts` (MIT, ISC, BSD, Apache-2.0, OFL-1.1); the build fails
  otherwise and writes `dist/THIRD_PARTY_NOTICES.txt`, which release archives
  and Linux packages ship. Keep `npm audit --omit=dev` clean.
