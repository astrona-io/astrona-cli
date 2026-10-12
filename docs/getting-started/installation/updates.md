# Staying up to date

```sh
astrona upgrade
```

`astrona upgrade` checks GitHub for the latest release, downloads the binary for your OS/architecture, verifies it against the SHA-256 digest GitHub records for it (and its [build provenance](../../guides/astrona-versions.md#build-provenance) when the GitHub CLI is installed), and atomically replaces the currently running executable — only when the release is newer than yours. On a [Homebrew](homebrew.md#homebrew-macos-linux) install it runs `brew upgrade astrona` instead (`brew reinstall astrona` with `--force`), so Homebrew keeps track of the install.

On DEB, RPM and Snap installs, `astrona upgrade` prints the package installation command and exits without modifying package-owned files, including with `--force`. Download and install the newer package using the [APT](apt.md), [DNF](dnf.md) or [Snap](snap.md) instructions.

astrona tells you when a newer release exists — `[INFO] astrona v0.2.3 is available (you have v0.2.2) — astrona upgrade · what's new: <release notes>` — at most **once a day**. It asks GitHub at most once a day too (cached in `~/.astrona/update-check.json`), so commands don't wait on the network. It never checks in CI (`CI`/`GITHUB_ACTIONS`/… set) or when stderr isn't a terminal; `ASTRONA_NO_UPDATE_CHECK=1` turns it off everywhere.

Older releases can be installed next to the latest — see [Astrona Versions](../../guides/astrona-versions.md).
