# Installation

Choose the installation method for your machine. Each page contains its own
commands and package-specific notes.

| Platform or method | Instructions |
|---|---|
| macOS or Linux with Homebrew | [Homebrew](installation/homebrew.md) |
| Debian or Ubuntu | [APT / DEB package](installation/apt.md) |
| Fedora or a Red Hat compatible distribution | [DNF / RPM package](installation/dnf.md) |
| Ubuntu with Snap | [Snap package](installation/snap.md) |
| Windows | [Windows and WSL 2 (preview)](installation/windows.md) |
| macOS or Linux, download script | [Quick install](installation/quick-install.md) |
| Download a binary yourself | [Manual install](installation/manual.md) |

## After installing

1. [Set up dependencies and verify your installation](installation/setup.md).
2. Optionally [enable shell completion](installation/shell-completion.md).
3. Follow the [Quickstart](quickstart.md) to run your first lab.

The CLI presents its [usage terms](../guides/cli-terms.md) before the first
operational command. Kind labs need a running Docker or Podman engine;
`astrona setup` helps prepare it.

## Manage your installation

- [Upgrade Astrona](installation/updates.md)
- [CLI preferences and settings review](../guides/settings.md)
- [Local data and cache cleanup](../guides/local-data.md)
