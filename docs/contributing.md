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

Versioning and deployment (on push to `main` and on release tags) are handled by [`.github/workflows/docs.yml`](https://github.com/astrona-io/astrona-cli/blob/main/.github/workflows/docs.yml) — see that file for the exact `mike deploy` invocations.

## Security-sensitive areas

If your change touches remote script execution, QEMU base image handling, remote config fetching, git config sources, or path resolution, read the "Security-sensitive areas" section of [`CLAUDE.md`](https://github.com/astrona-io/astrona-cli/blob/main/CLAUDE.md) first — these are explicit trust boundaries with existing conventions (HTTPS-only, size caps, path containment) that new code should follow.

## Testing

```sh
just check                                          # fmt + vet + unit tests
go build -o astrona ./cmd/astrona
./astrona test -c examples/k8s-basics-01 --junit-xml=report.xml   # e2e against a real kind cluster
```

## Dependency updates

[Renovate](https://docs.renovatebot.com/) (`.github/workflows/renovate.yml`, config in `.github/renovate.json5`) opens a PR for dependency updates every day, running as a GitHub App so its PRs get the full CI run. Go module **minor and patch** updates are set to auto-merge: GitHub merges them as soon as the required checks on `main` (Lint, Unit tests, E2E) pass, and never when they fail. Major updates, the `go`/`toolchain` directive and GitHub Actions updates wait for a human review. New releases are only proposed after they're 3 days old. The Dependency Dashboard issue lists everything pending.
