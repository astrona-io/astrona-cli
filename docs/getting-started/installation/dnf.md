# DNF (Fedora & Red Hat compatible distributions)

Download, verify and install the latest stable `.rpm` for your CPU:

```sh
(
  set -eu
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) echo "Unsupported CPU architecture" >&2; exit 1 ;;
  esac
  release_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/astrona-io/astrona-cli/releases/latest)"
  tag="${release_url##*/}"
  package="astrona-${tag#v}-linux-$arch.rpm"
  base="https://github.com/astrona-io/astrona-cli/releases/download/$tag"
  download_dir="$(mktemp -d)"
  cd "$download_dir"
  curl -fSL "$base/$package" -o "$package"
  curl -fsSL "$base/SHA256SUMS" -o SHA256SUMS
  awk -v file="$package" '$2 == file { print }' SHA256SUMS | sha256sum -c -
  sudo dnf install "./$package"
  astrona --version
)
```

DNF resolves Git and CA certificate dependencies. The RPM includes private Kind, kubectl and ORAS tools. Install and start Docker or Podman separately. QEMU, SSH and an ISO builder are optional VM dependencies; their package names and availability depend on your distribution and enabled repositories. CI checks installation on Fedora and AlmaLinux 9; a Red Hat subscription and enabled repositories may be needed on RHEL.

This is a local release package. A hosted DNF repository has not been configured, so `dnf install astrona` without the downloaded file will not find Astrona. Run the download-and-install block again to install the latest stable release, or install a newer release file manually. Remove with `sudo dnf remove astrona`.

Removing the package preserves `~/.astrona`. Destroy running labs before uninstalling.

## Next

[Set up and verify Astrona](setup.md), then [run your first lab](../quickstart.md).
