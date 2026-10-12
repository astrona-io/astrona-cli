# Snap (Ubuntu)

Download, verify and install the latest stable `.snap` for your CPU. Snap must already be installed on your system:

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
  package="astrona-${tag#v}-linux-$arch.snap"
  base="https://github.com/astrona-io/astrona-cli/releases/download/$tag"
  download_dir="$(mktemp -d)"
  cd "$download_dir"
  curl -fSL "$base/$package" -o "$package"
  curl -fsSL "$base/SHA256SUMS" -o SHA256SUMS
  awk -v file="$package" '$2 == file { print }' SHA256SUMS | sha256sum -c -
  sudo snap install --dangerous --classic "./$package"
  /snap/bin/astrona --version
)
```

The Snap bundles Kind, kubectl, ORAS, Git and CA certificates. Classic confinement lets lab scripts use your host files, kubeconfig and Docker/Podman sockets. Install and start a host container engine separately; optional VM tools also come from the host.

`--dangerous` is Snap's option for installing a local file without a Store assertion. Check the downloaded package against the release's `SHA256SUMS` first. This builds a local-installable Snap; publishing `snap install astrona --classic` requires registration of the Store name and approval for classic confinement. Locally installed Snaps do not update automatically. Installing a newer local file with the command above updates it. Remove with `sudo snap remove astrona`.

DEB, RPM and Snap packages preserve `~/.astrona` on removal. Destroy running labs before uninstalling. CI builds packages for both architectures, installs and reinstalls them with real package managers, checks bundled tool discovery and upgrade protection, then verifies removal preserves user data.

## Next

[Set up and verify Astrona](setup.md), then [run your first lab](../quickstart.md).
