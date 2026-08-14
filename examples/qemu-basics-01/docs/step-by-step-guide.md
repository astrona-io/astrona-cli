# Step-by-Step Guide: qemu-basics-01

> Declared in [`../config.yaml`](../config.yaml) under `metadata.docs.guide` — that field is the canonical pointer to this file.

This walks through the full solution to the [case study](./case-study.md) / [exam question](./exam-question.md). Read those first if you want to try it yourself before seeing the answer.

## 1. What this lab expects

Open [`../config.yaml`](../config.yaml) and look at four sections:

- `runtime` — `type: qemu`, so `astrona` boots a real VM with `qemu-system-x86_64` instead of a kind cluster. See [prerequisites](./prerequisites.md) for what needs to be installed and how to supply a base image.
- `bootstrap.init` — runs `hello-vm.sh` over SSH once the VM is up: writes `/tmp/astrona-lab/marker.txt` with the content `qemu-smoke-ok`, and prints the VM's hostname to prove SSH exec actually worked.
- `validation.script` — points at `validate.sh`, which reads that marker file back and fails unless its content is exactly `qemu-smoke-ok`. There's no `validation.checks` block, because those check kubectl resources and this lab has no kubectl target.
- `teardown` — just `keepCluster: false`; no teardown script in this lab.

## 2. Start the lab

From inside `examples/qemu-basics-01/` (after completing the [prerequisites](./prerequisites.md) — you need your own base image in place first):

```sh
astrona run -c .
```

This acquires and checksum-verifies your base image, boots the VM, waits for it to become SSH-ready, then runs `bootstrap.init`. You should see `hello from inside the qemu VM: astrona-lab` printed — that line only appears if it actually ran on the guest, not your host.

## 3. Satisfy the check (already done by bootstrap — here's how to verify it yourself)

`bootstrap.init` already wrote the marker file for you. If you want to connect to the VM yourself and look:

```sh
ssh -i ~/Library/Caches/astrona/qemu/qemu-basics-01/id_ed25519 \
    -p <port-printed-during-lab-up> \
    -o UserKnownHostsFile=~/Library/Caches/astrona/qemu/qemu-basics-01/known_hosts \
    student@127.0.0.1 \
    cat /tmp/astrona-lab/marker.txt
```

(On Linux, swap `~/Library/Caches` for `$XDG_CACHE_HOME` or `~/.cache`.) The SSH port is auto-picked per run and printed in `astrona run`'s output as `ssh-port=<N>`; the key and known_hosts live in that same per-lab state directory, which `astrona destroy` deletes entirely.

## 4. Submit to the Proctor

You don't grade yourself — you submit, and the Proctor grades, running `validate.sh` inside the VM over the same SSH connection:

```sh
astrona submit -c .
```

Expected output:

```
Submitting to the Proctor...
qemu SSH exec + file write verified inside the VM
  PASS  validation script (0.03s)

1 passed, 0 failed in 0.03s

PROCTOR: PASS
```

Exit code is `0` on pass, `1` on fail — if you're scripting this, check `$?` rather than parsing the text. For CI systems that want native test-result reporting instead of parsing stdout, add `--junit-xml=report.xml` to write a JUnit XML report GitHub Actions/GitLab CI/Jenkins can render directly.

## 5. Tear down

```sh
astrona destroy -c .
```

Sends the VM process `SIGTERM` (falling back to a forceful kill if it doesn't stop) and deletes its entire state directory — overlay disk, cloud-init seed, and the ephemeral SSH key all go with it. Nothing about this VM outlives `astrona destroy`.
