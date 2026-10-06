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

The newest astrona stays `astrona`; older ones are `astrona-<version>`. Downloads are verified against the **SHA-256 digest GitHub records for each release binary** — a mismatch is refused, and nothing unverified is ever made executable. Add `~/.astrona/bin` to your `PATH` to run e.g. `astrona-0.2.1` yourself.

## What happens when a lab needs another version

Every command that loads a lab checks its `astronaVersion`. If this astrona isn't allowed, it hands the whole command over to the **newest installed `astrona-<version>` that is** — you just keep typing `astrona`:

```
$ astrona test ./labs/old-lab
[INFO] this lab needs astrona <=0.2.1 — running it with astrona 0.2.1 (~/.astrona/bin/astrona-0.2.1)
…
PROCTOR: PASS
```

- The lab is passed on as `-c`/`-f`/`--git`/`--git-ref`, since an older version may not know [`astrona use`](../reference/cli/astrona_use.md) or a lab given as an argument. Flags the older version doesn't have make it fail with its own error.
- None installed? The error names the newest release that fits: `install it: astrona versions install 0.2.1`.
- `astrona-*` binaries in `~/.astrona/bin` are preferred; ones on your `PATH` count too.
- A developer build (no release number) runs every lab and doesn't hand over.

!!! note "astrona 0.2.1 and older don't know `astronaVersion`"
    They warn about an unknown field and otherwise run the lab normally — but their `astrona validate` reports it as an error. Validate such a lab with a current astrona.

## Upgrading

`astrona upgrade` installs the latest release over `astrona`, verified against the same GitHub digest, and only when it's actually newer than the one you have.
