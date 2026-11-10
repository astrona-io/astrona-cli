# qemu-basics-01 — step-by-step

A copy-pasteable walkthrough of this lab's full lifecycle: build the CLI,
bring the VM up, list it, SSH into it and do the task, check it, submit it
(which also tears it down). Run these from the repo root.

See [`docs/prerequisites.md`](docs/prerequisites.md) for what needs to be on
`PATH` first (`qemu-system-aarch64`, `qemu-img`, `ssh`, `oras`, an ISO9660
tool) — `astrona check` will tell you what's missing.

## 1. Build the CLI

```sh
just build
./astrona check
```

## 2. Bring the lab up

```sh
./astrona run -c examples/qemu-basics-01
```

This pulls the base image (an OCI artifact from
`ghcr.io/astrona-io/ubuntu-24.04-server-docker`, via `oras`), boots the VM,
waits for SSH + cloud-init, then runs the lab's bootstrap script inside it.
It returns as soon as that's done — the VM keeps running in the background,
it does not block your terminal.

The image is only ever pulled once: it's cached at
`~/.astrona/cache/images/`, named after the image source itself plus a
short hash for uniqueness — e.g.
`ubuntu-24.04-server-docker-arm64-sha256-015b0dd5ac43.qcow2` — so the
directory is browsable (`ls ~/.astrona/cache/images/`) instead of just a
pile of opaque hashes, even though the hash suffix (not the readable part)
is what actually identifies the entry: the full checksum is always
re-verified on every cache hit regardless of filename. Run `astrona run`
again (after `astrona submit` or `astrona destroy` deleted the VM, see
step 6) and you'll see `Using cached
qemu base image (sha256:...)` instead of a fresh `oras pull` — noticeably
faster on a ~700MB image. The cache survives `astrona destroy` (it's keyed
by checksum, not tied to any one lab run); delete
`~/.astrona/cache/images/` yourself if you ever want to force a re-pull.

## 3. See that it's running

```sh
./astrona list
```

Expect something like:

```
qemu VMs:
  ●  qemu-basics-01           pid=24313    ssh=student@127.0.0.1:54151  uptime=12s
```

Running this lab a second time (`astrona run` again) while it's already up
is refused on purpose — you'll get an "already running" error naming the
existing PID/SSH port instead of a second VM silently orphaning the first.
That's what used to cause ghost `qemu-system-*` processes piling up
invisibly; check `ps aux | grep qemu-system` if you ever want to confirm
none are left over.

## 4. SSH into it and do the task

```sh
./astrona ssh qemu-basics-01
```

`astrona ssh` takes the lab **name** (as shown by `astrona list`), not
`--config` — it doesn't need the lab config at all, just the running VM's
state. Drops you into an interactive shell as the `student` user
(passwordless sudo). Bootstrap only printed the VM's hostname; the marker
file is yours to write:

```sh
mkdir -p /tmp/astrona-lab
echo "qemu-smoke-ok" > /tmp/astrona-lab/marker.txt
cat /tmp/astrona-lab/marker.txt   # qemu-smoke-ok
exit
```

## 5. Check your work (optional)

```sh
./astrona submit -c examples/qemu-basics-01 --watch
```

Re-grades every 5 seconds while you work and prints ``Passing — run
`astrona submit` to record this attempt.`` once the check passes. It records
nothing, sends nothing and leaves the VM running; Ctrl-C stops it.

## 6. Submit

```sh
./astrona submit -c examples/qemu-basics-01
./astrona list   # should show "No astrona labs running."
```

Runs the validation script over SSH and prints PASS/FAIL (exit code `0`
on pass, `2` on fail). A submit is final: pass or fail, it records the
attempt, ends it and then deletes the lab, exactly like `astrona destroy`.
The qemu process is killed and its state dir under `~/.astrona/qemu/`
(overlay disk, cloud-init seed, ephemeral SSH keys) goes with it. To try
again, start a new attempt with `astrona run`.

## Stopping without submitting

```sh
./astrona destroy -c examples/qemu-basics-01
```

Deletes the VM the same way, without grading. Run it if you walk away
from a lab you don't submit; a qemu lab is a real background process
astrona manages itself, not something `docker ps` will remind you about
later.

## Multi-arch images (`{ARCH}`)

`config.yaml` intentionally leaves `runtime.qemu.arch` unset — it defaults
to *this host's own* architecture (Go's `runtime.GOARCH`, normalized to
`x86_64`/`aarch64`), so the VM boots with native acceleration (HVF on
Apple Silicon, KVM on Linux) on whichever machine runs it, instead of
silently defaulting to `x86_64` and falling back to slow software emulation.

`image.source` and `image.file` may contain the literal placeholder
`{ARCH}` — exact case, it's a literal substring replace, not a real
template engine — substituted with the resolved arch's OCI spelling
(`amd64` or `arm64`, not astrona's own `x86_64`/`aarch64`) before use. This
lab's `image.source` actually does this today:

```yaml
image:
  type: "oci"
  source: "ghcr.io/astrona-io/ubuntu-qcow2-image:24.04-base-{ARCH}"
  checksums:
    arm64: "sha256:015b0dd5ac43c07e2579c29af7858ce811d204986e399f736111c3c6cc48768f"
    # amd64: no ":24.04-base-amd64" tag published yet
```

Since a different arch is a genuinely different file with a genuinely
different hash, a templated image needs `checksums` (a map keyed the same
way `{ARCH}` resolves) instead of a single `checksum`. Running this on an
amd64 host today fails clearly at `resolveChecksum` ("no checksums entry for
arch 'amd64'") rather than a confusing registry 404 — there's no `amd64`
tag published for this image yet.

**`checksum`/`checksums` are both optional**, not enforced — you can omit
them entirely and astrona will still pull/boot the image. It isn't silent
about it though: every run prints `[WARN] ... has no checksum set —
booting it unverified and not caching it`, and that image is never written
to `~/.astrona/cache/images/` (there's no safe content-address to cache it
by without a checksum to have verified against) — so skipping this has a
real speed cost on top of the security one. Left set here anyway for
exactly that reason.

and the exact same `config.yaml` would pull and boot the right image on
both an Apple Silicon laptop and an amd64 CI runner without editing
anything. It isn't wired up that way today because only the `:arm64` tag
actually exists yet (`oras manifest fetch
ghcr.io/astrona-io/ubuntu-24.04-server-docker:amd64` 404s) — templating a
tag that doesn't exist would just trade a clear "not found" for a
confusing one.

## Troubleshooting: "can't SSH into the VM"

- Check it's actually up and get the right port: `astrona list`.
- Check `astrona run` actually finished (it waits for SSH readiness itself
  before returning) rather than being killed early.
- If you see `Host key verification failed`, the fix is `astrona destroy`
  then `astrona run` again — don't hand-edit
  `~/.astrona/qemu/qemu-basics-01/known_hosts`.
- `astrona ssh` only ever finds a match for a qemu lab — no other runtime
  writes to `~/.astrona/qemu`, so a name it can't find either isn't running
  or isn't a qemu lab.
