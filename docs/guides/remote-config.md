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
- The clone is cached under your user cache directory, keyed by a hash of the URL and ref as separate values. Repeat runs check the selected remote branch or tag's commit ID with `git ls-remote`. If that commit is already cached, no fetch is needed. Otherwise, Astrona fetches the selected ref and checks it out. No separate checksum file is required in the lab repository.
- A full commit hash stays pinned and needs no network check once cached. Other revision expressions, such as `main~1`, use the normal fetch path.
- If the metadata check or fetch fails, Astrona warns and uses the cached revision. A first-time clone still needs a connection. The selected commit is recorded in the cache's `refs/astrona/cached` Git ref; existing caches are supported without re-cloning.
- Before reusing a checkout, Astrona checks that its saved Git origin matches the requested repository. A mismatch in a current cache stops the command and keeps its files. An older cache with a different origin is left alone; a fresh checkout uses the new cache key. Matching older caches remain usable offline and by running labs.
- Astrona still restores tracked files and removes untracked and ignored files inside its managed cache on every run, even when no download is needed. Keep your own edits in a separate local checkout and use `-c` to run it.
- Both the selected path and the final config file must stay within the clone. This includes `--file` and the default `config.yaml`: symlinks outside the repository are refused when starting or reopening a saved run. Symlinks to files inside the repository remain supported. Local `-c` paths without `--git` keep their usual behavior.
- Progress says `Cloning …` for a new cache, then `Checking …` followed by `Using cached … (no download needed)` or `Updating …` on repeat runs. Progress goes to stderr so JSON output stays clean. Git's own checkout chatter is shown with `--verbose`, or on failure as part of the error.

## Precedence

`--git` is checked first: if set, it entirely determines how `--config` is interpreted (as a subdirectory, not a path/URL). Without `--git`, `--config` is checked in this order: a `https://`/`http://` prefix (used as a direct URL), otherwise treated as a local filesystem path (file or directory).

## Path safety

Every local path resolution — the lab's own base directory, script/manifest `source: file`/`folder` references — must be relative, is joined against the lab's base directory, and is rejected if it would escape it — lexically (`../..`) or through a symlink committed to the lab (`JoinWithinBaseDir`). Absolute paths are refused. A lab config can't reference files outside its own directory by accident or by a crafted `source` value.

## Approving remote labs

A lab from `--git` or a URL runs its scripts with bash on your machine (for kind labs), plus any `command` validation checks, and makes its `http` checks' requests from your machine. So before `run`, `reset`, `test` or `submit` executes anything from a remote lab for the first time, astrona shows what it will do and asks:

```text
First time running this lab:
  https://github.com/org/labs@main (labs/lab-01) (01eed0bb08be)

It will:
  • runs on this machine (bash): setup (setup.sh)
  • runs on this machine (command check): kubectl get ns shop -o name
  • requests from this machine (http check): http://web.localtest.me:8080/
  • applies 2 manifest source(s) to the lab cluster
  • opens 127.0.0.1:8080 → svc/web
  • ⚠ fetched when it runs, NOT covered by this approval: https://example.com/extra.sh

Only continue if you trust its author. Run it? [y/N]
```

- The approval is pinned: to the **git commit** for `--git`, to the **SHA-256 of the config** for a URL. Running the same version again doesn't ask; a new commit or a changed config asks again ("This lab changed since you approved it").
- Scripts and manifests with `type: url` are downloaded when they run, so their content isn't covered by the pin — they're flagged in the prompt.
- Local configs (`-c ./my-lab`) are your own files and never prompt.
- **Official catalog labs** are pre-trusted, but only for the exact commit Astrona published — see [Official labs](#official-labs).
- Everything the lab supplies to the prompt (names, URLs, commands, resource descriptions) is shown on one line with control characters stripped, so a lab can't hide or fake lines of the prompt with terminal escape sequences or newlines. Resource listings (`astrona run`, `astrona docs`, `astrona res`) are cleaned the same way.
- `astrona destroy` asks too, before running a remote lab's teardown scripts (only if it has any). Declining — or no terminal and no `--trust` — doesn't stop the destroy: the teardown scripts are skipped with a warning and the lab is removed anyway.
- **CI / no terminal:** without a terminal, an unapproved remote lab is refused. Pass `--trust` to approve it — e.g. `astrona test --git https://github.com/org/labs --config labs/lab-01 --trust`. CI that tests its own checkout (`-c .`) needs nothing.
- Approvals are stored in `~/.astrona/trust.json`; delete an entry (or the file) to be asked again.
- A lab whose `astronaVersion` sends it to another installed astrona is approved here first, before the hand-over — never by the version it asks for (see [Astrona Versions](astrona-versions.md#what-happens-when-a-lab-needs-another-version)).

### Official labs

A lab you run by its catalog name from an official training (a repository in the `astrona-io` GitHub organization, see [Lab Catalog](lab-catalog.md)) runs without the prompt when its content is exactly what Astrona published. Trust is bound to the content, never to the name or URL alone. All of these must hold:

- you named it from the catalog (`astrona run astrona.io/ATS014/…`), and it is fetched from `https://github.com/astrona-io/<repository>`;
- the checked-out **git commit** equals the commit astrona.io publishes for that training — the commit Astrona synced the training's catalog entry from, read from `https://astrona.io/api/labs` over HTTPS when the lab is checked;
- the checkout has no local changes.

astrona then prints one line on stderr and goes ahead:

```text
Trusted as an official Astrona lab at 3f9c2e7a0d41b6c8e5f2a9d7b1c4e6f8a0b2d4c6 (published by https://astrona.io).
```

Anything else falls back to the normal prompt, with a line saying why: a different commit (a push astrona.io hasn't synced yet, another ref), a training with no published commit, astrona.io not reachable, local changes, a fork or another host, or a lab from a source you added. Only `https://astrona.io` vouches for official labs — a site set with `ASTRONA_URL` or a sign-in elsewhere never does (except in an unversioned development build, for testing a local stack). Nothing is written to `~/.astrona/trust.json`: the next run checks again. `--trust` works as before; without a terminal (`--headless`, CI) an official lab at its published commit runs, and any other remote lab still needs `--trust`.
