# Contributing

## Requirements

- Go (version pinned in [`go.mod`](https://github.com/astrona-io/astrona-cli/blob/main/go.mod))
- [`just`](https://github.com/casey/just)
- [`kind`](https://kind.sigs.k8s.io/), `kubectl`, Docker or Podman — for actually running a lab
- [`uv`](https://docs.astral.sh/uv/) — for building/serving these docs (nothing else Python-related needed; `uv run` handles the rest)

## Justfile recipes

| Recipe | What it does |
|---|---|
| `just build` | `go build -o astrona ./cmd/astrona` |
| `just install` | Build, then move the binary onto `PATH` (`~/.local/bin` by default, override with `ASTRONA_INSTALL_DIR`) |
| `just clean` | Remove the built binary |
| `just fmt` | Fail if any file needs `gofmt` |
| `just fmt-fix` | Auto-fix formatting |
| `just vet` | `go vet ./...` |
| `just test` | `go test ./...` |
| `just check` | `fmt` + `vet` + `test` — run before committing |
| `just docs-gen-cli` | Regenerate `docs/reference/cli/` from the actual `astrona` command tree |
| `just docs` | Serve the docs site locally at `http://127.0.0.1:8000` with live reload |
| `just build-docs` | Build the static docs site (`mkdocs build --strict`) — what CI runs |

Run `just` with no arguments to list every recipe.

## Working on the docs

This site is [MkDocs](https://www.mkdocs.org/) + [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/), versioned with [mike](https://github.com/jimporter/mike). Source lives under `docs/`, config at the repo root `mkdocs.yml`.

```sh
just docs
```

opens a live-reloading local copy. The CLI reference under `docs/reference/cli/` is **generated** (via `astrona docgen`, a hidden command wired into `just docs`/`just build-docs`) — don't hand-edit it, edit the command's `Short`/`Long`/flag descriptions in `cmd/astrona/cmd_*.go` instead.

**Adding a command:** put it in a help group in `cmd/astrona/groups.go` (`astrona --help` lists commands by group, in workflow order) and add its generated page(s) to the matching group of the CLI Reference in `mkdocs.yml`. Unit tests fail if either is missing.

### Theme and browser checks

The docs use the frontend's Mission Control palette and typography. The matching
tokens live in `docs/stylesheets/extra.css`; their source is
`astrona-web/app/assets/css/_tokens.scss`. `docs/overrides/main.html` adds the
shared background and motion control. `home.html` contains the homepage hero;
the guide links and feature descriptions stay in `docs/index.md`.

Material still owns search, code copying, mobile navigation and mike version
selection. Keep overrides small and use the `url` template filter for internal
links so release-specific paths work. Do not hand-edit generated command pages.
Space Grotesk and JetBrains Mono are requested from Google Fonts, as on the
frontend; system fonts remain available when those requests are blocked.

Decorative SVGs are hidden from assistive technology. Content is visible without
JavaScript. Motion must respect `prefers-reduced-motion` and the saved pause
choice in `docs/javascripts/motion.js`; do not make animation part of navigation
or hide content until an animation runs.

Build the site, then check it in Chromium:

```sh
just build-docs
uv run --with playwright playwright install chromium
uv run --with playwright python .github/scripts/check-docs-theme.py \
  --site-dir site --screenshots /tmp/astrona-docs-screenshots
```

Use `--browser /path/to/chromium` to reuse an installed browser. The check starts
a temporary localhost server under `/dev/` and checks desktop/375px layouts,
navigation, search, copy controls, reduced motion and a no-JavaScript page.
It does not run labs or contact Astrona's services. Screenshots stay outside the
repository. Also check the deployed mike version selector after a release.

Versioning and deployment (on push to `main` and on release tags) are handled by [`.github/workflows/docs.yml`](https://github.com/astrona-io/astrona-cli/blob/main/.github/workflows/docs.yml) — see that file for the exact `mike deploy` invocations.

## Security-sensitive areas

If your change touches remote script execution, QEMU base image handling, remote config fetching, git config sources, or path resolution, read the "Security-sensitive areas" section of [`CLAUDE.md`](https://github.com/astrona-io/astrona-cli/blob/main/CLAUDE.md) first — these are explicit trust boundaries with existing conventions (HTTPS-only, size caps, path containment) that new code should follow.

## Testing

```sh
just check                                          # fmt + vet + unit tests
go build -o astrona ./cmd/astrona
./astrona test -c examples/k8s-basics-01 --junit-xml=report.xml   # e2e against a real kind cluster
```

The Ubuntu catalog integration test runs the first published lab without logging in:

```sh
bash .github/scripts/test-catalog-lab.sh ./astrona /tmp/astrona-catalog-diagnostics
```

For Podman, pass `podman` as the third argument and run with rootful Podman (for example, `sudo env "PATH=$PATH" bash .github/scripts/test-catalog-lab.sh ./astrona /tmp/astrona-catalog-diagnostics podman`). The script explicitly selects the requested engine using `KIND_EXPERIMENTAL_PROVIDER`.

It needs Linux, Docker or Podman, Kind and kubectl, plus network access to GitHub and Docker Hub. It isolates credentials and kubeconfig, explicitly accepts running without account tracking via `--without-login`, verifies node readiness, repairs the first lab's intentional image typo with `imagePullPolicy: Never` to confirm the Alpine preload, then destroys the lab and checks that its cluster, containers, context and saved state are gone. Failures retain logs and diagnostics and trigger cleanup. CI runs it on Ubuntu 24.04 with Docker's classic and containerd image stores and with rootful Podman.

## Dependency updates

[Renovate](https://docs.renovatebot.com/) — Mend's hosted GitHub App, configured by `.github/renovate.json5` — opens PRs for dependency updates; they get the full CI run like any other PR (jobs and logs: [developer.mend.io](https://developer.mend.io)). Go module **minor and patch** updates are set to auto-merge: GitHub merges them as soon as the required checks on `main` (Lint, Unit tests, E2E) pass, and never when they fail. Major updates, the `go`/`toolchain` directive and GitHub Actions updates wait for a human review. New releases are only proposed after they're 3 days old. The Dependency Dashboard issue lists everything pending.

## Releases

Pushing a tag `vX.Y.Z` runs `.github/workflows/release.yml`: it builds the four binaries with build provenance, publishes the GitHub release as **latest**, updates the [Homebrew tap](https://github.com/astrona-io/homebrew-tap) and then installs from the tap on macOS and Linux to check it.

A tag with a `-` — a release candidate like `v0.4.0-rc1` — is published as a **pre-release**: it never becomes "latest", so `astrona upgrade`, the update notice and Homebrew keep offering the last stable release, and the tap isn't touched. Testers install an RC on purpose with `astrona versions install 0.4.0-rc1`.
