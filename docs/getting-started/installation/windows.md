# Windows (preview)

Labs need Linux, so on Windows astrona runs inside [WSL 2](https://learn.microsoft.com/windows/wsl/) (Linux inside Windows). `astrona.exe` is a small launcher that sets that up and then forwards every command into it — you keep typing `astrona …` in PowerShell.

Requirements: Windows 10 22H2 or Windows 11, virtualization turned on in your BIOS/UEFI, and [Docker Desktop](https://www.docker.com/products/docker-desktop/).

In PowerShell:

```powershell
irm https://github.com/astrona-io/astrona-cli/releases/latest/download/astrona-windows-amd64.exe -OutFile $env:LOCALAPPDATA\astrona.exe
& $env:LOCALAPPDATA\astrona.exe setup
```

(`astrona-windows-arm64.exe` on ARM PCs.) `astrona setup` on Windows, one step at a time — run it again after a restart or a manual step, and finished steps are skipped:

1. **WSL 2** — `wsl --install --no-distribution` if WSL is missing (Windows asks for admin; restart afterwards).
2. **Ubuntu** — `wsl --install -d Ubuntu`; a window asks you to choose a Linux username and password. Set `ASTRONA_WSL_DISTRO` to use another distribution.
3. **Docker** — checks `docker info` works inside Ubuntu. If not, install Docker Desktop, start it, and turn on *Settings → Resources → WSL integration* for Ubuntu ([guide](https://docs.docker.com/desktop/features/wsl/)).
4. **astrona inside Ubuntu** — the Linux astrona, into `~/.local/bin` (same release as the launcher; a development build of `astrona.exe` says so and installs the latest release). The download is checked against that release's `SHA256SUMS` before it is made executable; if the checksum is missing or does not match, nothing is installed.
5. **kind and kubectl** — runs `astrona setup` inside Ubuntu.
6. **PATH** — copies `astrona.exe` to `%LOCALAPPDATA%\Programs\astrona` and adds that folder to your *user* PATH (no admin).

After that, any command — `astrona labs`, `astrona run …`, `astrona submit …` — runs inside Ubuntu, with the same output and exit codes (`astrona submit` still exits 2 for "graded, didn't pass"). Your arguments are passed through as they are, never re-parsed by a shell (the launcher uses `wsl --exec`, which skips the distribution's default shell). `astrona --launcher-version` prints the launcher's own version.

## Next

[Set up and verify Astrona](setup.md), then [run your first lab](../quickstart.md).
