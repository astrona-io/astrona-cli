# Step-by-Step Guide: qemu-basics-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.solution` — that field is the canonical pointer to this file.

This walks through the full solution to the [case study](./case-study.md) / [exam question](./exam-question.md). Read those first if you want to try it yourself before seeing the answer.

## 1. What this lab expects

Open [`../config.yaml`](../config.yaml) and look at four sections:

- `runtime` — `type: qemu`, so `astrona` boots a real VM with QEMU instead of a kind cluster. See [prerequisites](./prerequisites.md) for what needs to be installed and how the base image is supplied.
- `bootstrap.init` — runs `hello-vm.sh` over SSH once the VM is up. It only prints the VM's hostname, to prove SSH exec actually worked. It does **not** create `/tmp/astrona-lab` or the marker file — that's your task.
- `validation.script` — points at `validate.sh`, which fails with `expected a file at /tmp/astrona-lab/marker.txt, but it does not exist` until you've written the file, and fails again unless its content is exactly `qemu-smoke-ok`. There's no `validation.checks` block, because those check kubectl resources and this lab has no kubectl target.
- `teardown` — just `keepCluster: false`; no teardown script in this lab.

## 2. Start the lab

From inside `examples/qemu-basics-01/` (after completing the [prerequisites](./prerequisites.md)):

```sh
astrona run -c .
```

This acquires the base image, boots the VM, waits for it to become SSH-ready, then runs `bootstrap.init` — you'll see `✓ hello-vm` under `Bootstrap`. The script's own output (`hello from inside the qemu VM: astro-qemu-basics-01`, the VM's hostname) goes to the full log whose path `astrona run` prints at the end; add `--verbose` to stream it to your terminal instead. That line only appears if it actually ran on the guest, not your host.

## 3. Satisfy the check

Open a shell inside the VM:

```sh
astrona ssh qemu-basics-01
```

`astrona ssh` takes the lab name (as `astrona list` shows it), not `-c`, and logs you in as the `student` user with the lab's own ephemeral key — no key paths or ports to look up. You'll see `Connecting to 'astro-qemu-basics-01' (student@127.0.0.1:<port>)...` and then a shell prompt.

Now do the task. `/tmp` is world-writable (with the sticky bit, as on any Linux box), so `student` can create the directory without `sudo`:

```sh
mkdir -p /tmp/astrona-lab
echo "qemu-smoke-ok" > /tmp/astrona-lab/marker.txt
cat /tmp/astrona-lab/marker.txt
```

The last command should print exactly `qemu-smoke-ok`. Type `exit` to leave the VM; it keeps running.

## 4. Check before you submit (optional)

A submit is final, so check first:

```sh
astrona submit -c . --watch
```

This re-grades every 5 seconds (`--interval` changes that) and records, sends and deletes nothing. Once the file is right you'll see:

```
  ✓ verify-marker-file

Score: 1/1 (100%)
Passing — run `astrona submit` to record this attempt.
```

Press Ctrl-C to stop watching. You can leave it running in a second terminal while you work in `astrona ssh`.

## 5. Submit to the Proctor

You don't grade yourself — you submit, and the Proctor grades, running `validate.sh` inside the VM over SSH:

```sh
astrona submit -c .
```

Expected output (timings and attempt numbers will differ):

```
Proctor
qemu SSH exec + file write verified inside the VM
  PASS  verify-marker-file (0.14s)

1 passed, 0 failed in 0.14s
Score: 1/1 points (100%)

PROCTOR: PASS
  ✓ Tear down qemu VM "astro-qemu-basics-01" (0.1s)
Lab astro-qemu-basics-01 deleted.
```

A submit is final: pass or fail, it records the attempt, ends it and then deletes the lab — the VM process is stopped and its whole state directory (overlay disk, cloud-init seed, ephemeral SSH keys) goes with it, exactly like `astrona destroy`. There's nothing left to tear down. If you failed, start a new attempt with `astrona run -c .` and do the task again; `astrona submit --history -c .` lists your previous attempts.

Exit code is `0` on pass, `2` on a graded fail (anything else is a CLI error) — if you're scripting this, check `$?` rather than parsing the text. For CI systems that want native test-result reporting instead of parsing stdout, add `--junit-xml=report.xml` to write a JUnit XML report GitHub Actions/GitLab CI/Jenkins can render directly.

## Leaving without submitting

If you want to walk away from the lab without grading it:

```sh
astrona destroy -c .
```

Sends the VM process `SIGTERM` (falling back to a forceful kill if it doesn't stop) and deletes its entire state directory. Nothing about this VM outlives `astrona destroy`.
