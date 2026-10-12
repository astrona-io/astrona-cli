# Shell completion

Installed with Homebrew? Completion for bash, zsh and fish is already set up — skip the steps below (Homebrew's shell setup must be loaded, see `brew info astrona`).

`astrona completion <shell>` prints a completion script for bash, zsh, fish or PowerShell. Besides commands and flags, it completes **lab names** from what's actually on your machine — `astrona destroy <TAB>`, `stop`, `start`, `shell`, `kubeconfig`, `diagnose`, `ssh`, `port-forward list|stop` — filtered to what makes sense (`start` offers stopped labs, `ssh` qemu VMs), each with its runtime and status. Type part of the name without `astro-` and the short form is completed.

It also completes:

- `--cluster <TAB>` (`shell`, `kubeconfig`, `net`, `reset`) — the lab's [linked clusters](../../guides/linked-labs.md)
- `astrona versions install <TAB>` — published releases you don't have yet; `versions remove <TAB>` — the installed ones
- a lab directory or config file wherever a command takes one (`astrona use ./<TAB>`, `astrona run ./<TAB>`)

=== "zsh"

    ```sh
    astrona completion zsh > "${fpath[1]}/_astrona"   # then start a new shell
    ```

=== "bash"

    ```sh
    astrona completion bash > ~/.local/share/bash-completion/completions/astrona
    ```

=== "fish"

    ```sh
    astrona completion fish > ~/.config/fish/completions/astrona.fish
    ```
