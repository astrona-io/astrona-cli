# Linux packages

`release.yml` calls `linux-packages.yml` before publishing a release. Release
assets include DEB, RPM and classic Snap packages for Linux amd64 and arm64;
their hashes are included in the release's `SHA256SUMS`.

The reusable workflow also runs on relevant pull requests. DEB/RPM installs
are checked on Ubuntu 24.04, Debian 12, Fedora 42 and AlmaLinux 9. Snap is built
and installed on disposable Ubuntu 24.04 runners with snapd. Checks cover
private tool lookup, Git/HTTPS access, package-owned upgrade protection, reinstall and removal
without deleting user data. Actual Kubernetes lab lifecycle tests remain in
the Kind integration workflows.

Build a native package:

```sh
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0
bash .github/scripts/build-linux-package.sh deb v0.3.0 amd64 dist
bash .github/scripts/build-linux-package.sh rpm v0.3.0 arm64 dist
```

Build a Snap in Canonical's [core24 Snapcraft container](https://github.com/canonical/snapcraft-rocks), matching the host architecture:

```sh
SNAPCRAFT_CONTAINER_ENGINE=docker bash .github/scripts/build-linux-package.sh snap v0.3.0 amd64 dist
```

`podman` is also supported. The engine variable is an executable name or path,
not a shell command. Without this variable, the script uses installed
Snapcraft in destructive mode: use only a disposable Ubuntu 24.04 build
machine, since Snapcraft can install build dependencies on the host.

Tool versions are pinned in `tools.env`; downloads are verified against the
upstream release checksums before packaging. Update these pins deliberately,
review the upstream release notes and run the package and Kind integration
workflows. Kind/kubectl/ORAS binaries and their Apache licenses are included.
The CLI uses sibling `tools/` inside packages and never installs or replaces
host binaries named `kind`, `kubectl` or `oras`.

Native dependencies are limited to Git and CA certificates. A container
engine is the user's choice, and QEMU/SSH/ISO tools are optional dependencies
for VM labs. There are no network-fetching installation hooks.

These are local-install release assets, not hosted package repositories.
To offer `apt-get install astrona` / `dnf install astrona`, provision signed
APT/RPM repositories and document their keys and sources. Snap Store release
requires ownership of the `astrona` name, classic-confinement approval and
publishing credentials. The workflow does not register accounts or publish
to the Store.
