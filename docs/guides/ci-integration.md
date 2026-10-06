# CI Integration

`astrona test` is built for CI: bootstrap → apply the reference solution → submit to the Proctor → always tear down, in one command, one exit code. This is what astrona-cli's own [`ci.yml`](https://github.com/astrona-io/astrona-cli/blob/main/.github/workflows/ci.yml) e2e job runs against `examples/k8s-basics-01` on every PR.

## Minimal GitHub Actions job

```yaml
name: Test lab

on:
  pull_request:

jobs:
  test-lab:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7

      - name: Install kubectl
        run: |
          version="$(curl -L -s https://dl.k8s.io/release/stable.txt)"
          curl -LO "https://dl.k8s.io/release/${version}/bin/linux/amd64/kubectl"
          chmod +x kubectl
          sudo mv kubectl /usr/local/bin/kubectl

      - name: Install kind
        run: go install sigs.k8s.io/kind@v0.32.0

      - name: Install astrona
        run: |
          curl -L -o astrona "https://github.com/astrona-io/astrona-cli/releases/latest/download/astrona-linux-amd64"
          chmod +x astrona

      - name: astrona check
        run: ./astrona check

      - name: astrona validate
        run: ./astrona validate -c .

      - name: astrona test
        run: ./astrona test -c . --junit-xml=junit-report.xml --diagnostics-dir=astrona-diagnostics

      - name: Upload JUnit report
        if: always()
        uses: actions/upload-artifact@v7
        with:
          name: junit-report
          path: junit-report.xml

      - name: Upload diagnostics
        if: failure()
        uses: actions/upload-artifact@v7
        with:
          name: astrona-diagnostics
          path: astrona-diagnostics/
```

Notes:

- `if: always()` on the upload step — a failing `astrona test` still writes the JUnit report, and you want that artifact whether the check passed or not.
- Testing a lab from **another** repo or a URL (`--git …` / `-c https://…`) needs `--trust` in CI — remote labs must be approved before they run (see [Approving remote labs](remote-config.md#approving-remote-labs)). Testing the repo's own checkout (`-c .`) doesn't.
- Docker is already available on GitHub-hosted `ubuntu-latest` runners, which is all the `kind` runtime needs. A `qemu` runtime lab needs `/dev/kvm` for real hardware acceleration — GitHub-hosted runners don't have it, so a qemu-backed `astrona test` there would fall back to slow software emulation. Run qemu labs' CI on a self-hosted runner with KVM, or a GitHub-hosted runner that provides it.
- Labs hitting Docker Hub rate limits in CI can list their images in [`runtime.kind.preloadImages`](../concepts/runtimes.md#preloaded-images-runtimekindpreloadimages): each image is then pulled once per job on the runner instead of once per node (and per restarted pod).
- `astrona test --repeat 3` catches flaky checks before students do — each run uses a fresh environment, so it's slower; a nightly job is a good place for it.
- `--diagnostics-dir` puts the [diagnostics bundle](#diagnostics-on-failure) inside the workspace so the `if: failure()` step can upload it.
- `astrona test` always tears down its own environment on exit (even on failure or a cancelled step, best-effort), so a CI job doesn't need its own cleanup step.

## Diagnostics on failure

A failed CI run used to leave nothing behind but "FAIL" — `astrona test` tears the cluster down on exit. Now, when it fails, it first collects a diagnostics bundle (before teardown) and prints the unhealthy pods straight into the job log:

```text
Diagnostics bundle: astrona-diagnostics
  Unhealthy pods (2):
    default/crasher: app: Error (exit 3); app restarted 2×; app: not ready
    default/web-7c76977dcb-nxbfw: web: ErrImagePull (failed to pull and unpack image "docker.io/library/does-not-exist:1": … repository does not exist …)
  12 warning event(s) — see summary.md.
```

The bundle (typically ~1 MB):

| Path | Contents |
|---|---|
| `summary.md` | Unhealthy pods with their reason, newest warning events |
| `cluster/` | `kubectl get` nodes / pods / workloads / events (+ Gateway API resources if installed), `describe nodes` |
| `pods/` | `describe` and logs (plus `--previous` logs after a restart) for every unhealthy pod |
| `kind-logs/` | `kind export logs`: node journal, kubelet, containerd, every pod's log files |
| `vms/` | qemu labs: each VM's serial console log |
| `clusters/<cluster>/` | Labs with [linked clusters](linked-labs.md): the same bundle (`summary.md`, `cluster/`, `pods/`, `kind-logs/`) for each linked cluster — often where a failing lab's real problem is |
| `versions.txt`, `errors.txt` | Tool versions; anything that couldn't be collected |

Secrets, ConfigMaps and kubeconfigs are never collected. Pod `describe` output does include environment variables set inline in a pod spec — keep lab credentials in Secrets, and keep CI artifacts private.

| Flag | Default | |
|---|---|---|
| `--diagnostics` | `on-failure` | `always` to collect on success too, `never` to skip |
| `--diagnostics-dir` | `~/.astrona/diagnostics/<lab>-<timestamp>` | Where to write the bundle |

The same bundle can be collected from a running lab with [`astrona diagnose`](../reference/cli/astrona_diagnose.md).

## Other CI systems

The same commands (`astrona check`, `astrona validate -c <path>`, `astrona test -c <path> --junit-xml=<path> --diagnostics-dir=<path>`, upload the XML and — on failure — the diagnostics directory) work anywhere that can run a Linux binary and understands JUnit XML — GitLab CI (`artifacts: reports: junit:`), Jenkins (`junit` post-build step), etc.

## Exit codes

`astrona test` (and `astrona submit`) exit `0` only on a Proctor PASS. A missing dependency, a failed bootstrap step, or a FAIL verdict all exit non-zero — no extra flag needed to make CI fail correctly.
