# Quick install (macOS & Linux)

```sh
mkdir -p ~/.local/bin && \
OS=$(uname -s | tr '[:upper:]' '[:lower:]') && \
ARCH=$(uname -m) && \
[ "$ARCH" = "x86_64" ] && ARCH="amd64" || true && \
[ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ] && ARCH="arm64" || true && \
curl -L -o ~/.local/bin/astrona "https://github.com/astrona-io/astrona-cli/releases/latest/download/astrona-${OS}-${ARCH}" && \
chmod +x ~/.local/bin/astrona
```

Make sure `~/.local/bin` is on your shell's `PATH`.

## Next

[Set up and verify Astrona](setup.md), then [run your first lab](../quickstart.md).
