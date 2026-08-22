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

Only `https://` is accepted for a lab config fetched over the network — astrona refuses a plain `http://` config URL outright. The download is size-capped (10 MiB) so a misbehaving or malicious server can't exhaust disk or memory.

## Git repository (`--git`)

```sh
astrona run --git https://github.com/org/labs-repo.git -c labs/k8s-basics-01
astrona run --git git@github.com:org/labs-repo.git --git-ref feature-branch -c .
```

With `--git` set, `--config` changes meaning: instead of a local path or URL, it becomes the **subdirectory within the cloned repo** to use (`.` for the repo root). `--git` accepts anything `git clone` does — `https://`, `git@host:`, or `ssh://` — so authentication (SSH agent, credential helper) is entirely git's own, astrona doesn't handle credentials itself.

- `--git-ref` checks out a specific branch, tag, or commit (default: the repo's own default branch).
- The clone is cached under your user cache directory, keyed by a hash of the URL + ref — a repeat run reuses it and just fetches/checks out again rather than re-cloning.
- The resolved subdirectory is validated to stay within the clone — a `--config` value like `../../etc` is rejected, not silently escaped.

## Precedence

`--git` is checked first: if set, it entirely determines how `--config` is interpreted (as a subdirectory, not a path/URL). Without `--git`, `--config` is checked in this order: a `https://`/`http://` prefix (used as a direct URL), otherwise treated as a local filesystem path (file or directory).

## Path safety

Every local path resolution — the lab's own base directory, script/manifest `source: file`/`folder` references — is joined against the lab's base directory and rejected if it would escape it (`JoinWithinBaseDir`). A lab config can't reference files outside its own directory by accident or by a crafted `source` value.
