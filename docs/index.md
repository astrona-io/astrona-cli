---
title: Overview
---

# Astrona CLI

Astrona spins up local Kubernetes labs with [kind](https://kind.sigs.k8s.io/), backed by whichever container runtime you have — Docker or Podman — or as a QEMU virtual machine when a lab needs a full OS instead of a container. Everything about a lab — its cluster, its bootstrap steps, its grading rules — is driven by a single YAML config file.

Astrona is the CLI behind [astrona.io](https://astrona.io)'s hands-on labs, and it works the same way whether a lab config comes from a local directory, a URL, or a git repository — so anyone can author and run their own labs without the platform.

## What it does

- **Local kind clusters** — auto-detects Docker or Podman on your `PATH` and creates/deletes the kind cluster accordingly, no manual provider flags needed.
- **QEMU VM labs** — for labs that need a real OS (not just a container), `runtime.type: qemu` boots one or more VMs from a base image, with SSH wired up automatically.
- **Config from anywhere** — point `--config`/`-c` at a local directory, a local file, an `http(s)://` URL, or a git repository via `--git`.
- **Bootstrap** — run init scripts and apply Kubernetes manifests when a lab starts.
- **Testing stage** — a CI-only stage that applies a reference solution, so a lab author can prove their own lab is actually solvable and passes grading before publishing it.
- **Proctor grading** — a student never grades their own work: `astrona submit` hands the environment to the [Proctor](concepts/grading.md), which runs declarative checks and/or custom scripts and returns a PASS/FAIL verdict.
- **CI-friendly** — `astrona test` runs the whole bootstrap → testing → submit → teardown pipeline in one command, with `--junit-xml` output for any CI system that understands JUnit.

## Where to go next

<div class="grid cards" markdown>

- **New to Astrona?** Start with [Installation](getting-started/installation.md), then run through the [Quickstart](getting-started/quickstart.md).
- **Building a lab?** [Authoring a Lab](guides/authoring-a-lab.md) walks through the full `config.yaml` shape with a worked example.
- **Looking up a flag or a YAML field?** [CLI Reference](reference/cli/astrona.md) and [Lab Config Schema](reference/lab-config.md) are generated straight from the source.
- **Wiring astrona into CI?** [CI Integration](guides/ci-integration.md) covers `astrona test` and JUnit reporting.

</div>

## Project links

- Source: [github.com/astrona-io/astrona-cli](https://github.com/astrona-io/astrona-cli)
- Platform: [astrona.io](https://astrona.io)
- License: Apache License 2.0
