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

      - name: astrona test
        run: ./astrona test -c . --junit-xml=junit-report.xml

      - name: Upload JUnit report
        if: always()
        uses: actions/upload-artifact@v7
        with:
          name: junit-report
          path: junit-report.xml
```

Notes:

- `if: always()` on the upload step — a failing `astrona test` still writes the JUnit report, and you want that artifact whether the check passed or not.
- Docker is already available on GitHub-hosted `ubuntu-latest` runners, which is all the `kind` runtime needs. A `qemu` runtime lab needs `/dev/kvm` for real hardware acceleration — GitHub-hosted runners don't have it, so a qemu-backed `astrona test` there would fall back to slow software emulation. Run qemu labs' CI on a self-hosted runner with KVM, or a GitHub-hosted runner that provides it.
- `astrona test` always tears down its own environment on exit (even on failure or a cancelled step, best-effort), so a CI job doesn't need its own cleanup step.

## Other CI systems

The same three commands (`astrona check`, `astrona test -c <path> --junit-xml=<path>`, upload the XML) work anywhere that can run a Linux binary and understands JUnit XML — GitLab CI (`artifacts: reports: junit:`), Jenkins (`junit` post-build step), etc.

## Exit codes

`astrona test` (and `astrona submit`) exit `0` only on a Proctor PASS. A missing dependency, a failed bootstrap step, or a FAIL verdict all exit non-zero — no extra flag needed to make CI fail correctly.
