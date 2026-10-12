---
title: Overview
template: home.html
hide:
  - navigation
  - toc
---

<div class="astro-home-intro" markdown>

Astrona is the CLI behind [astrona.io](https://astrona.io)'s hands-on labs, and it works the same way whether a lab config comes from a local directory, a URL, or a git repository — so anyone can author and run their own labs without the platform.

</div>

## Where to go next

<div class="grid cards" markdown>

- <svg class="astro-card-icon" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M24 5C15 13 15 22 17 30H31C33 22 33 13 24 5Z"/><circle cx="24" cy="19" r="4"/><path d="M17 23L10 33L17 31M31 23L38 33L31 31M20 36L24 43L28 36"/></svg>

    **New to Astrona?** Start with [Installation](getting-started/installation.md), then run through the [Quickstart](getting-started/quickstart.md).

- <svg class="astro-card-icon" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M24 7L39 15V33L24 41L9 33V15Z M9 15L24 24L39 15 M24 24V41"/><circle cx="24" cy="7" r="3"/><circle cx="9" cy="33" r="3"/><circle cx="39" cy="33" r="3"/></svg>

    **Building a lab?** [Authoring a Lab](guides/authoring-a-lab.md) walks through the full `config.yaml` shape with a worked example.

- <svg class="astro-card-icon" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><circle cx="24" cy="24" r="16"/><ellipse cx="24" cy="24" rx="23" ry="8" transform="rotate(-35 24 24)"/><path d="M17 21L21 24L17 27M26 28H32"/></svg>

    **Looking up a flag or a YAML field?** [CLI Reference](reference/cli/astrona.md) and [Lab Config Schema](reference/lab-config.md) are generated straight from the source.

- <svg class="astro-card-icon" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="5" y="16" width="12" height="16" rx="3"/><rect x="31" y="16" width="12" height="16" rx="3"/><path d="M17 24H31M22 19L27 24L22 29M11 10V5H37V10M11 38V43H37V38"/></svg>

    **Wiring astrona into CI?** [CI Integration](guides/ci-integration.md) covers `astrona test` and JUnit reporting.

</div>

## What it does

<div class="astro-capabilities" markdown>

- **Local kind clusters** — auto-detects Docker or Podman on your `PATH` and creates/deletes the kind cluster accordingly, no manual provider flags needed.
- **QEMU VM labs** — for labs that need a real OS (not just a container), `runtime.type: qemu` boots one or more VMs from a base image, with SSH wired up automatically.
- **Config from anywhere** — point `--config`/`-c` at a local directory, a local file, an `http(s)://` URL, or a git repository via `--git`.
- **Bootstrap** — run init scripts and apply Kubernetes manifests when a lab starts.
- **Testing stage** — a CI-only stage that applies a reference solution, so a lab author can prove their own lab is actually solvable and passes grading before publishing it.
- **Proctor grading** — a student never grades their own work: `astrona submit` hands the environment to the [Proctor](concepts/grading.md), which runs declarative checks and/or custom scripts and returns a PASS/FAIL verdict.
- **CI-friendly** — `astrona test` runs the whole bootstrap → testing → submit → teardown pipeline in one command, with `--junit-xml` output for any CI system that understands JUnit.

</div>

## Project links

- Source: [github.com/astrona-io/astrona-cli](https://github.com/astrona-io/astrona-cli)
- Platform: [astrona.io](https://astrona.io)
- License: Apache License 2.0
