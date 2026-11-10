# Local data and cache cleanup

## Automatic cache limits

Disposable caches have a default retention of **30 days** and a combined maximum
size of **50 GB**. Change them with:

```sh
astrona settings set cache-retention-days 30
astrona settings set cache-max-size-gb 50
astrona settings set cache-max-size-gb 0  # unlimited size; age limit still applies
```

Astrona checks the limits when `astrona run` or `astrona test` finishes, including
when it fails after setup. These are the commands that download images, addons
and Git checkouts; other commands never delete cache entries. It deletes expired entries first, then the oldest remaining
entries until their combined size fits the budget. Age uses filesystem modification
times; for a Git checkout, the newest entry within it determines its age. Reading
a cache file alone does not reset its age. Images and their metadata are removed
together. GB means 1,000,000,000 bytes, measured as logical file sizes.

The limits cover downloaded images/addons, cached Git checkouts, catalog metadata,
update-check metadata and [VM state left by older versions](#vm-state-from-older-versions).
They do not cover lab disks, student resources, results,
logs, bundles, installed tools, credentials, or Docker/Podman storage.

This is cleanup after lab runs, not a background service or a download quota.
Nothing expires while the CLI is not running. If any saved lab state exists,
including a stopped VM, **all cache eviction is deferred** to protect dependencies.
Cleanup never waits: if any other CLI command is running, it is skipped and
tried again after a later run, so it never delays the command you started. Usage
can therefore exceed the limits while downloads or labs need it. A notice on
stderr explains deferrals and reports removed entries without changing JSON
stdout or the original command's exit code. Help, completions, settings and data
inspection skip release checks, so cleanup remains available without contacting
GitHub.

Preview or explicitly apply just the configured policy:

```sh
astrona data clean --policy
astrona data clean --policy --yes
astrona data clean --policy --yes --dry-run  # preview only
```

Settings changes take effect after the next `run` or `test`, or on policy cleanup;
changing a setting does not itself delete anything. Policy cleanup needs valid
settings. Ordinary `data clean` and `data reset` remain usable for repair when
settings are malformed. `--policy` cannot be combined with `--temp-only`.

## Inspect and clean manually

Inspect local files without connecting to Astrona or starting a container engine:

```sh
astrona data
astrona data -o json
```

The report lists file counts and logical byte sizes under `~/.astrona` and the
Astrona Git cache (`~/Library/Caches/astrona/repos` on macOS, normally
`~/.cache/astrona/repos` on Linux). It includes saved results, logs, VM state and
installed CLI versions, but does not read their contents or follow symlinks.
Logical sizes can exceed actual disk use for sparse VM disks. Docker/Podman
storage and progress saved to your account are outside this report.

Preview cleanup before applying it:

```sh
astrona data clean
astrona data clean --yes
astrona data clean --temp-only
astrona data clean --temp-only --yes
```

Cleanup removes downloaded image/addon caches, cached Git checkouts, and the
catalog/update-check metadata. Those files will need to be downloaded again;
check that you no longer need them offline. `--temp-only` limits deletion to
partial image/addon downloads older than 24 hours inside Astrona's cache. It
does not scan the system temp directory or delete unrelated temporary files.

If any saved lab, VM, Kind, exam or port-forward state exists, caches are kept.
Stopped VMs can still depend on cached backing images. Use `astrona list` to
inspect your labs and `astrona destroy <lab>` to remove those you no longer need.
Do not delete state files to bypass this check. Symlinked cache locations are
rejected, and symlinks at the top of a cache are kept for manual inspection.

### VM state from older versions

Older Astrona versions could leave a VM's state directory under
`~/.astrona/qemu/<vm>/` without a `handle.json`. No command can reach such a VM,
but its disks (large qcow2 files) would stay forever and keep caches from being
cleaned. `astrona data clean` lists such a directory, with its size, as
`VM state from an older Astrona` and removes it with `--yes` when all of these hold:

- it is a real directory directly under `~/.astrona/qemu` (symlinks are kept and
  never followed);
- it has no `handle.json`;
- nothing in it changed for 24 hours, so a lab that is still starting is left alone;
- no `qemu-system` process started with `-name astrona-<vm>` is running. If one
  is, or processes cannot be listed, the directory is kept and the preview says so.

Once removed, it no longer counts as saved lab state, so caches can be cleaned in
the same run. Automatic cleanup after `run` and `test` treats these directories
like other disposable entries: they are removed once older than
`cache-retention-days` or when the size limit is exceeded. `astrona destroy <lab>`
removes the lab's own directory straight away, under the same process check.
`--temp-only` never touches VM state.

Ordinary commands hold a shared maintenance lock, so they can run side by side;
confirmed cleanup or reset requires an exclusive lock. A command that finds the
lock taken prints `Waiting for another Astrona command to finish with local
data...` on stderr and waits up to 5 seconds before it gives up with an error.
Cleanup or reset therefore fails while a lab command such as `astrona run` is
still in progress; finish it first. The background helpers that only report
usage, send saved results or keep port forwards running do not take the lock; a
playground's time-limit watcher takes it only while it destroys the playground.
The browser workspace server (`astrona ui serve`, started by `astrona run`)
holds the shared lock while it runs and stops by itself only after 15 minutes
idle, so confirmed cleanup or reset stops it first, the same way `astrona ui stop`
does, and says so in one line on stderr. Its browser terminals close; labs keep
running. If the server cannot be stopped, the command fails and asks you to run
`astrona ui stop`.
Older CLI releases do not take this lock. Finish those commands before applying
maintenance. The `.maintenance.lock` and `settings.json.lock` files stay in place;
their presence does not mean a process is running. Never delete them to unlock
the CLI.

Reset saved CLI preferences and the selected lab:

```sh
astrona data reset
astrona data reset --yes
```

Reset deletes only `settings.json`, `settings-review.json`, `current.json`, `update-check.json`, and
`catalog.json`. It works even if a settings file contains invalid JSON. This
restores preference defaults and clears the `astrona use` selection; it does not
restart or destroy a lab. Credentials, trust approvals, custom catalog sources,
results, resources, bundles, logs and installed tools are preserved. Use
`astrona logout` separately if you want to sign out.

Both commands accept `-o json`. Without `--yes` they only preview; `--dry-run`
always prevents deletion, even when combined with `--yes`. The CLI cannot restore
deleted files. To remove old logs or unused CLI releases, use the existing commands:

```sh
astrona logs clean --older-than 168h --keep 10
astrona versions list
```
