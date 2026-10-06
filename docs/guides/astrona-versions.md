# Astrona Versions

A lab is written against an astrona release. When a later astrona changes behavior the lab relies on, the lab can say which releases may run it — and astrona runs it with one of those, installed side by side.

## Declaring it: `astronaVersion`

```yaml
apiVersion: astrona.io/v1
astronaVersion: "<=0.2.1"          # or ">=0.2.0, <0.3.0", "0.2.1", "!=0.2.2"
metadata:
  name: my-lab
```

Comparisons are `=`, `!=`, `>`, `>=`, `<`, `<=`, separated by commas (all must hold); a bare version means exactly that one. Versions are `major.minor.patch` (a leading `v` is fine; `0.3.0-rc1` sorts before `0.3.0`). Omit the field and any astrona runs the lab.

(`apiVersion` is a different thing: the *format* of the config file. `astronaVersion` is which *releases* may run the lab.)

## Older versions side by side

```sh
astrona versions available          # published releases
astrona versions install 0.2.1      # → ~/.astrona/bin/astrona-0.2.1
astrona versions list               # this astrona + the installed ones
astrona versions remove 0.2.1
```

The newest astrona stays `astrona`; older ones are `astrona-<version>`. Downloads are verified against the **SHA-256 digest GitHub records for each release binary** — a mismatch is refused, and nothing unverified is ever made executable.

### Build provenance

Releases after v0.2.2 also carry a signed **build provenance attestation**: proof that the binary was built by astrona-cli's own release workflow from this repository. When the [GitHub CLI](https://cli.github.com) (`gh`, logged in) is installed, `astrona versions install` and `astrona upgrade` check it too:

| Result | What happens |
|---|---|
| Verified | installed — "build provenance verified" |
| Release ≤ v0.2.2 (built before attestations) | installed — "this release predates build provenance attestations" |
| Newer release **without** its attestation, or one that doesn't verify | **refused** — the binary may have been replaced |
| No `gh`, or not logged in | installed on the SHA-256 check alone, with a hint |

To check a download yourself: `gh attestation verify astrona-darwin-arm64 --repo astrona-io/astrona-cli`, or `sha256sum -c SHA256SUMS` with the checksum file attached to each release. Add `~/.astrona/bin` to your `PATH` to run e.g. `astrona-0.2.1` yourself.

## What happens when a lab needs another version

Commands that run, test, grade or validate a lab (`run`, `test`, `submit`, `reset`, `validate`, `status`, …) check its `astronaVersion`. If this astrona isn't allowed, it hands the whole command over to the **newest installed `astrona-<version>` that is** — you just keep typing `astrona`:

```
$ astrona test ./labs/old-lab
[INFO] this lab needs astrona <=0.2.1 — running it with astrona 0.2.1 (~/.astrona/bin/astrona-0.2.1)
…
PROCTOR: PASS
```

- The lab is passed on as `-c`/`-f`/`--git`/`--git-ref`, since an older version may not know [`astrona use`](../reference/cli/astrona_use.md) or a lab given as an argument. Flags the older version doesn't have make it fail with its own error.
- **None installed?** astrona never downloads anything just because a lab config says so — a config may come from someone else's repository. Instead:
    - **At a terminal it asks first:**
      ```
      This lab needs astrona <=0.2.1 (this is 0.2.2). Download and install astrona 0.2.1,
      verified against GitHub's SHA-256 digest? [y/N]
      ```
      Yes installs the newest release that fits and continues the same command with it; no (the default) stops, and tells you the `astrona versions install …` command for later.
    - **Without a terminal** (CI, scripts) it stops with that command — or pass **`--install-version`** to install it without asking.
- **A remote lab is approved first.** For a lab from `--git` or a URL, the [trust prompt](remote-config.md#approving-remote-labs) happens in *this* astrona, before anything is installed or handed over — for every command that hands over, not only `run`/`submit`/`test`/`reset`. The approval is stored in `~/.astrona/trust.json`, so the version it's handed to doesn't ask again. If this astrona can't even parse the lab's config (it was written for another version), it can't show what the lab does, so there's no prompt: the lab must already be approved at this exact version, or you pass `--trust` after reviewing it.
- **Never older than 0.2.0.** astrona never hands a lab over to — or installs for one — a release older than 0.2.0, the first with trust prompts: a lab asking for one could otherwise get its scripts run without your approval. A lab that only fits an older release stops with an explanation; if you trust it, install that version yourself and run it as `astrona-<version>`.
- Only `astrona-*` binaries in `~/.astrona/bin` (where `astrona versions install` puts them) are used — ones elsewhere on your `PATH` are ignored, so a stray binary never runs a lab.
- `astrona docs` and `astrona destroy` don't check: reading a lab's docs doesn't depend on the version, and cleaning up must always work.
- A developer build (no release number) runs every lab and doesn't hand over.

!!! note "astrona 0.2.1 and older don't know `astronaVersion`"
    They warn about an unknown field and otherwise run the lab normally — but their `astrona validate` reports it as an error. Validate such a lab with a current astrona.

## Upgrading

`astrona upgrade` installs the latest release over `astrona`, verified against the same GitHub digest, and only when it's actually newer than the one you have. Installed with Homebrew? Then `astrona upgrade` runs `brew upgrade astrona` (`brew reinstall astrona` with `--force`) instead of downloading the binary itself, so Homebrew keeps track of the install. `astrona versions install` works the same either way (it installs into `~/.astrona/bin`, not Homebrew's directory).
