# Contributing

## Requirements

- Go (version pinned in [`go.mod`](https://github.com/astrona-io/astrona-cli/blob/main/go.mod))
- [`just`](https://github.com/casey/just)
- [`kind`](https://kind.sigs.k8s.io/), `kubectl`, Docker or Podman — for actually running a lab
- [`uv`](https://docs.astral.sh/uv/) — for building/serving these docs (nothing else Python-related needed; `uv run` handles the rest)

## Justfile recipes

| Recipe | What it does |
|---|---|
| `just build` | Build the development binary at `./astrona`; nothing is installed or moved |
| `just run <args>` | Build and run `./astrona`, forwarding CLI arguments |
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

```sh
just build
./astrona --help
./astrona settings path
./astrona run ./examples/k8s-basics-01 --ui
```

Or build and run in one step:

```sh
just run --help
just run settings path
just run run ./examples/k8s-basics-01 --ui
```

`./cmd/astrona` is the Go source directory, not the executable. Running it
directly can report `permission denied`; use `./astrona` after `just build`,
`just run <args>`, or `go run ./cmd/astrona <args>` instead.

Use `./astrona` to run your local build. Plain `astrona` may run the installed
release from your `PATH`. The former `just install` recipe has been removed.

Unversioned builds (including plain `go build` and `go run`) store their
credentials, settings, acceptance receipts, lab state and caches under
`~/.astrona-dev`. Git clones are in `~/.astrona-dev/cache/repos`. They use
`astrodev-` cluster names (`astrodev-test-` for authoring tests), so release
discovery and `destroy '*'` do not select development clusters, or vice versa.
First use requires its own terms acceptance. Settings review and login are optional.
Existing release data is neither copied nor migrated.

Tagged releases, built with `-X main.Version=vX.Y.Z`, keep `~/.astrona`, the
existing OS Git cache and `astro-` cluster names. Do not stamp a local test
binary with a release version unless you intend to use release data.
Development builds disable update checks, self-upgrade and installation of
other release binaries; rebuild from source to update them. Background lab
helpers run the same binary and keep the same profile.

This isolates Astrona-managed state, not the machine: container engines, images,
host ports and your kubectl configuration are still shared. Port collisions
remain possible. Lab scripts still run on your machine. Development builds
default to `http://localhost:3000`; release builds default to `https://astrona.io`.
`astrona login --site`, `ASTRONA_URL`, or a site remembered with a development
sign-in can select another server. `ASTRONA_URL` overrides the remembered site;
the login flag overrides it for sign-in. Logging in and submitting can affect
the account on the selected server. On Windows, build and run inside WSL. An unversioned
native launcher refuses to forward commands to the installed release.

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

If your change touches remote script execution, QEMU base image handling, remote config fetching, git config sources, or path resolution, read [`AGENTS.md`](https://github.com/astrona-io/astrona-cli/blob/main/AGENTS.md) and the relevant [security reference](https://github.com/astrona-io/astrona-cli/blob/main/agent-notes/security.md) first — these are explicit trust boundaries with existing conventions (HTTPS-only, size caps, path containment) that new code should follow.

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

It needs Linux, Docker or Podman, Kind and kubectl, plus network access to GitHub and Docker Hub. It isolates credentials and kubeconfig, uses `--headless` for headless setup without a new account session, verifies node readiness, repairs the first lab's intentional image typo with `imagePullPolicy: Never` to confirm the Alpine preload, then destroys the lab and checks that its cluster, containers, context and saved state are gone. Failures retain logs and diagnostics and trigger cleanup. CI runs it on Ubuntu 24.04 with Docker's classic and containerd image stores and with rootful Podman.

## Dependency updates

[Renovate](https://docs.renovatebot.com/) — Mend's hosted GitHub App, configured by `.github/renovate.json5` — opens PRs for dependency updates; they get the full CI run like any other PR (jobs and logs: [developer.mend.io](https://developer.mend.io)). Renovate merges its PRs after checks pass, using its own merge process rather than GitHub's native auto-merge. New releases are only proposed after they're 3 days old. The Dependency Dashboard issue lists everything pending.

When a same-repository `renovate[bot]` PR from a `renovate/` branch merges into
`main`, **Renovate patch release** creates the next stable patch tag at that exact
merge commit. For example, `v0.4.6` becomes `v0.4.7`, then `v0.4.8`. Only the final
number changes, regardless of whether the dependency update itself is major,
minor, patch, or a digest. Pre-release tags do not affect this calculation.
Ordinary PRs and closed, unmerged PRs do not trigger a release.

The workflow queues merges and runs the existing binary, Linux package,
Homebrew, and versioned documentation pipelines. It calls these workflows
explicitly because [a tag created with `GITHUB_TOKEN` does not trigger another
push workflow](https://docs.github.com/en/actions/concepts/security/github_token).
No new token is needed; Homebrew publication still uses the
existing `HOMEBREW_TAP_TOKEN` secret.

Retry a failed run from Actions: it reuses its existing patch tag. An older merge
already included in a later stable tag is skipped to avoid releasing older source
as a newer version. Tags are never moved or overwritten. The first stable tag
must already exist. The allocation policy tests run with
`python3 -B -m unittest discover -s .github/scripts -p 'test_renovate_release.py'`.

## Releases

Pushing a tag `vX.Y.Z` runs `.github/workflows/release.yml`: it builds the four binaries with build provenance, publishes the GitHub release as **latest**, updates the [Homebrew tap](https://github.com/astrona-io/homebrew-tap) and then installs from the tap on macOS and Linux to check it.

A tag with a `-` — a release candidate like `v0.4.0-rc1` — is published as a **pre-release**: it never becomes "latest", so `astrona upgrade`, the update notice and Homebrew keep offering the last stable release, and the tap isn't touched. Testers install an RC on purpose with `astrona versions install 0.4.0-rc1`.
