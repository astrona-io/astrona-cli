# macOS Kind startup regression

The opt-in **macOS Kind coexistence** workflow (`.github/workflows/macos-kind.yml`)
uses a self-hosted Apple Silicon Mac with Docker Desktop running under a
standard account, outside the `admin` group. Install Go, Kind and kubectl for
that user and label the runner `astrona-kind-standard-user`. Run it manually
with `workflow_dispatch` on a reviewed branch; it does not execute pull-request
code automatically. Do not use sudo to run the test.

The test creates two ordinary Kind clusters (one with a worker), then runs
`ATS013/section-010/module-01/playground` signed out with explicit
`--without-login` confirmation. It verifies API-server health, node readiness,
CoreDNS rollout and a service DNS lookup from a pod. After destroying the lab,
it checks that its containers and lab state are gone, the prior kubectl context
is restored, and both existing fixture clusters are still healthy with the same
container IDs. Its exit trap removes the fixtures and preserves failure logs.
It refuses existing clusters whose names would collide with the test.

To run locally:

```sh
go build -o /tmp/astrona ./cmd/astrona
ASTRONA_REQUIRE_MACOS_STANDARD_USER=1 bash .github/scripts/test-kind-coexistence.sh /tmp/astrona /tmp/macos-kind-diagnostics docker
```

The same script can run with `podman` on macOS or Linux without the standard-user
assertion. That exercises coexistence, DNS and teardown, but does not validate
Docker Desktop or prove that a non-admin macOS account works. Ubuntu coverage
remains in the ordinary CI workflow.

Astrona state and kubeconfig are isolated. The test preserves the caller's
Docker configuration and XDG configuration so per-user runtime connections
still work. Allow room for four new nodes, plus your existing workloads.
Failure diagnostics include only the test's clusters; existing user clusters
are never selected for deletion. Raw logs can contain lab script output, so
review them before sharing outside your team.

## Kernel keyring quota failures

When multiple clusters share a rootless engine, its Linux kernel keyring quota
can prevent pod sandboxes from starting. The event contains
`unable to create session key: disk quota exceeded`; this refers to kernel keys,
not disk storage. A local macOS/Podman regression run reproduced this failure
with the default 200-key quota. The automatic diagnostics preserved the events
and all test clusters were removed.

`astrona check` and the startup checks read quota usage through an existing
Kind control-plane node, including nodes created outside Astrona. They warn
about small limits or nearly exhausted quotas. With no running Kind node the
VM quota cannot be read this way; failure diagnostics still recognize the
event and explain it. These checks never change kernel settings.

Ask the Linux VM/host administrator to review `kernel.keys.maxkeys` and
`kernel.keys.maxbytes`, or reduce concurrent clusters. On macOS these settings
belong to the engine's Linux VM, not macOS. Kind's upstream issue suggests
`maxkeys=20000` and `maxbytes=500000` for rootless workloads:
[Kind issue #3806](https://github.com/kubernetes-sigs/kind/issues/3806).
A rootful engine connection has different quotas and can be tested separately;
that does not establish non-admin Docker Desktop support. Increasing the DNS
timeout cannot resolve an exhausted kernel quota.
