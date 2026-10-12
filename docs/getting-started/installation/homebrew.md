# Homebrew (macOS & Linux)

```sh
brew install astrona-io/tap/astrona
```

The [tap](https://github.com/astrona-io/homebrew-tap) is updated by every release, pinned to the same binaries and SHA-256 values as the GitHub release. CI installs the formula with real Homebrew on macOS and Linux for every change that could affect it, and again from the tap right after each release. Upgrade with `brew upgrade astrona`, or `astrona upgrade` — on a Homebrew install it runs `brew upgrade astrona` for you instead of downloading the binary itself.

What the Homebrew install gives you on top of the binary:

- **Lab tools are installed automatically**: `kind`, `kubectl` (`kubernetes-cli`), `git` for catalog/repository labs, `oras` for OCI VM images, `qemu` for VM labs, and `cdrtools` (`mkisofs`) for cloud-init seed images. Homebrew resolves and installs their dependencies too. Docker or Podman you install and start yourself — e.g. `brew install podman && podman machine init && podman machine start`, or Docker Desktop.
- **Shell completion** for bash, zsh and fish is set up — no `astrona completion` step (see [Shell completion](shell-completion.md#shell-completion)).
- **Man pages**: `man astrona`, `man astrona-run`, … one per command.
- **No Gatekeeper prompt** on macOS: a binary downloaded in a browser gets the quarantine flag and macOS may refuse to run it; one installed by Homebrew doesn't.

Two things to know:

- An older `astrona` earlier on your `PATH` (e.g. `~/.local/bin/astrona` from the quick install) hides the Homebrew one — `which -a astrona` shows the order; remove the old one.
- `brew uninstall astrona` removes the program, not `~/.astrona` (lab state, results, approvals, installed versions). Destroy running labs first (`astrona destroy <lab>`); delete `~/.astrona` yourself if you want it gone.

## Next

[Set up and verify Astrona](setup.md), then [run your first lab](../quickstart.md).
