# CLI usage terms

The first operational command, including plain `astrona`, displays the CLI usage
terms and data-loss risk notice on stderr. Answer `y` or `yes` to continue.
Blank input, `no`, or EOF cancels the command before lab downloads, sign-in or
setup. Read the notice without accepting it:

```sh
astrona terms
```

To accept explicitly, including in automation:

```sh
astrona terms --accept-terms
astrona test --accept-terms -c ./labs/my-lab
```

Acceptance is saved in `~/.astrona/terms.json` with the notice revision, SHA-256,
UTC timestamp, CLI version and acceptance method. This is local state, not a
server-side audit or account record. A newer CLI asks again if its bundled
notice changes, even if its revision was not bumped. Updating only the website
does not change an installed CLI's notice. CLI updates with unchanged terms do
not ask again.

Without a terminal, missing or outdated acceptance fails with instructions;
piping `yes` is not sufficient. Detected CI requires `--accept-terms` on every
operational command, even with a saved receipt. The flag accepts the terms in
the binary being run, so review them when upgrading a pipeline's pinned CLI.
`--yes`, `--trust`, and saved settings never imply terms acceptance.

Help, version output, completions and `astrona terms` remain available without
acceptance. So do `settings`, `data`, `logout`, `stop`, `destroy`, and
`port-forward stop`, so declining cannot prevent cleanup or preference repair.
Their other safety checks still apply. Internal documentation generators and
already-started background supervisors do not prompt.

Cache cleanup and `data reset` preserve the receipt. To forget it, remove only
`~/.astrona/terms.json`; the next operational command will ask again. A malformed
receipt also needs fresh acceptance. A symlink or directory at that path is
refused rather than replaced. Failure to save acceptance stops the command.

On Windows, native setup records acceptance in the Windows user's `.astrona`
directory before changing WSL. Labs keep a separate receipt inside the chosen
WSL distribution. Read the launcher's notice with `astrona setup --terms`, even
before WSL is installed; `astrona terms` shows the Linux CLI's notice in WSL.
`astrona setup --yes --accept-terms` supplies both approvals;
without the flag, first-time setup can ask once on each side.

The CLI remains Apache-2.0 licensed. This notice describes risks and points to
the existing license; it does not guarantee immunity from liability. Maintainers
should obtain legal review before relying on the wording as a legal agreement.
