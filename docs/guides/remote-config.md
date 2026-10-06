# Remote Config Sources

`--config`/`-c` (default `.`) and `--file`/`-f` (default `config.yaml`) are persistent flags shared by every command. Where they point isn't limited to a local directory.

## Local directory or file

```sh
astrona run -c path/to/lab           # looks for path/to/lab/config.yaml
astrona run -c path/to/lab/lab.yaml  # -f defaults to config.yaml, but an explicit file path is used as-is
astrona run -c path/to/lab -f lab.yaml
```

## Direct URL

```sh
astrona run -c https://example.com/labs/k8s-basics-01/config.yaml
astrona run -c https://example.com/labs/k8s-basics-01/          # -f appended: .../config.yaml
```

Only `https://` is accepted for a lab config fetched over the network — astrona refuses a plain `http://` config URL outright, and refuses to follow a redirect from an `https://` URL to an `http://` one. The download is size-capped (10 MiB) so a misbehaving or malicious server can't exhaust disk or memory.

## Git repository (`--git`)

```sh
astrona run --git https://github.com/org/labs-repo.git -c labs/k8s-basics-01
astrona run --git git@github.com:org/labs-repo.git --git-ref feature-branch -c .
```

With `--git` set, `--config` changes meaning: instead of a local path or URL, it becomes the **subdirectory within the cloned repo** to use (`.` for the repo root). `--git` accepts anything `git clone` does — `https://`, `git@host:`, or `ssh://` — so authentication (SSH agent, credential helper) is entirely git's own, astrona doesn't handle credentials itself.

- `--git-ref` checks out a specific branch, tag, or commit (default: the repo's own default branch).
- The clone is cached under your user cache directory, keyed by a hash of the URL + ref — a repeat run reuses it and just fetches/checks out again rather than re-cloning.
- The resolved subdirectory is validated to stay within the clone — a `--config` value like `../../etc` is rejected, not silently escaped.
- Only a one-line `Cloning …` / `Updating …` is printed; git's own checkout chatter is shown with `--verbose`, or on failure as part of the error.

## Precedence

`--git` is checked first: if set, it entirely determines how `--config` is interpreted (as a subdirectory, not a path/URL). Without `--git`, `--config` is checked in this order: a `https://`/`http://` prefix (used as a direct URL), otherwise treated as a local filesystem path (file or directory).

## Path safety

Every local path resolution — the lab's own base directory, script/manifest `source: file`/`folder` references — is joined against the lab's base directory and rejected if it would escape it (`JoinWithinBaseDir`). A lab config can't reference files outside its own directory by accident or by a crafted `source` value.

## Approving remote labs

A lab from `--git` or a URL runs its scripts with bash on your machine (for kind labs), plus any `command` validation checks. So before `run`, `reset`, `test` or `submit` executes anything from a remote lab for the first time, astrona shows what it will do and asks:

```text
First time running this lab:
  https://github.com/org/labs@main (labs/lab-01) (01eed0bb08be)

It will:
  • runs on this machine (bash): setup (setup.sh)
  • runs on this machine (command check): kubectl get ns shop -o name
  • applies 2 manifest source(s) to the lab cluster
  • opens 127.0.0.1:8080 → svc/web
  • ⚠ fetched when it runs, NOT covered by this approval: https://example.com/extra.sh

Only continue if you trust its author. Run it? [y/N]
```

- The approval is pinned: to the **git commit** for `--git`, to the **SHA-256 of the config** for a URL. Running the same version again doesn't ask; a new commit or a changed config asks again ("This lab changed since you approved it").
- Scripts and manifests with `type: url` are downloaded when they run, so their content isn't covered by the pin — they're flagged in the prompt.
- Local configs (`-c ./my-lab`) are your own files and never prompt.
- `astrona destroy` asks too, before running a remote lab's teardown scripts (only if it has any). Declining — or no terminal and no `--trust` — doesn't stop the destroy: the teardown scripts are skipped with a warning and the lab is removed anyway.
- **CI / no terminal:** without a terminal, an unapproved remote lab is refused. Pass `--trust` to approve it — e.g. `astrona test --git https://github.com/org/labs --config labs/lab-01 --trust`. CI that tests its own checkout (`-c .`) needs nothing.
- Approvals are stored in `~/.astrona/trust.json`; delete an entry (or the file) to be asked again.
- A lab whose `astronaVersion` sends it to another installed astrona is approved here first, before the hand-over — never by the version it asks for (see [Astrona Versions](astrona-versions.md#what-happens-when-a-lab-needs-another-version)).
