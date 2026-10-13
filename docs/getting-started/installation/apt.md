# APT (Debian & Ubuntu)

Download the latest stable `.deb`, verify its SHA-256 checksum, and install it. This detects `amd64` (Intel/AMD) or `arm64` (ARM) automatically. You need `curl`, `sha256sum`, and permission to use `sudo`.

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
  package="astrona-${tag#v}-linux-$arch.deb"
  base="https://github.com/astrona-io/astrona-cli/releases/download/$tag"
  download_dir="$(mktemp -d)"
  cd "$download_dir"
  curl -fSL "$base/$package" -o "$package"
  curl -fsSL "$base/SHA256SUMS" -o SHA256SUMS
  awk -v file="$package" '$2 == file { print }' SHA256SUMS | sha256sum -c -
  sudo apt-get install "./$package"
  astrona --version
)
```

The commands below for DNF and Snap use the same download and verification process. Each resolves a single release tag so the package and its checksums come from the same release. These packages become available after the first release containing the Linux packaging feature; a missing asset stops the commands before installation. Downloaded files remain in the temporary directory; the subshell keeps your current directory unchanged.

APT installs Git and CA certificates automatically. The package includes pinned, checksum-verified **Kind, kubectl and ORAS**, installed privately under `/usr/lib/astrona/tools`. Astrona and its lab scripts use those tools without replacing tools you already have on your shell's PATH. `astrona shell` uses the tools in its isolated terminal container.

Install and start **Docker or Podman** separately. For optional VM labs on Debian/Ubuntu:

```sh
sudo apt-get install qemu-system-x86 qemu-system-arm qemu-utils genisoimage openssh-client
```

This is a local release package. A hosted APT repository has not been configured, so `apt-get install astrona` without the downloaded file will not find Astrona. Repeat the download-and-install commands to upgrade. Remove with `sudo apt-get remove astrona`; this preserves `~/.astrona`. Destroy running labs before uninstalling.

## Next

[Set up and verify Astrona](setup.md), then [run your first lab](../quickstart.md).
