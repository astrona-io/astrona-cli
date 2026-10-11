#!/usr/bin/env bash
# Build verified, self-contained CLI tools into a DEB, RPM or classic Snap.
# Requires Go, curl, sha256sum, tar; nfpm for DEB/RPM, snapcraft for Snap.
# SNAPCRAFT_CONTAINER_ENGINE=docker (or podman) builds Snap in Canonical's
# disposable core24 image instead of installing Snapcraft on the host.
set -euo pipefail
format="${1:?usage: build-linux-package.sh <deb|rpm|snap> <vX.Y.Z> <amd64|arm64> <output-dir>}"
tag="${2:?missing version}"
arch="${3:?missing architecture}"
output="${4:?missing output directory}"
case "$format" in deb|rpm|snap) ;; *) echo "Unsupported format: $format" >&2; exit 1;; esac
case "$arch" in amd64|arm64) ;; *) echo "Unsupported architecture: $arch" >&2; exit 1;; esac
[[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9]+([.-][a-zA-Z0-9]+)*)?$ ]] || { echo 'Invalid release tag' >&2; exit 1; }
root="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$root"
# shellcheck source=../../packaging/tools.env
source packaging/tools.env
mkdir -p "$output"
output="$(cd "$output" && pwd)"
work="$(mktemp -d "$output/.astrona-package.XXXXXX")"
trap 'rm -rf "$work"' EXIT
stage="$work/stage"
mkdir -p "$stage/tools" "$stage/licenses"
fetch() { curl --fail --silent --show-error --location --retry 3 --proto '=https' --proto-redir '=https' "$1" -o "$2"; }
verify() {
  local checksum="$1" file="$2" actual
  [[ "$checksum" =~ ^[0-9a-f]{64}$ ]] || { echo "Missing/invalid checksum for $file" >&2; exit 1; }
  actual="$(sha256sum "$file" | awk '{print $1}')"
  [[ "$actual" == "$checksum" ]] || { echo "Checksum mismatch for $file" >&2; exit 1; }
}
kind_url="https://github.com/kubernetes-sigs/kind/releases/download/$KIND_VERSION"
fetch "$kind_url/kind-linux-$arch" "$stage/tools/kind"
fetch "$kind_url/kind-linux-$arch.sha256sum" "$work/kind.sha256"
verify "$(awk '{print $1}' "$work/kind.sha256")" "$stage/tools/kind"
kubectl_url="https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/$arch/kubectl"
fetch "$kubectl_url" "$stage/tools/kubectl"
fetch "$kubectl_url.sha256" "$work/kubectl.sha256"
verify "$(cat "$work/kubectl.sha256")" "$stage/tools/kubectl"
oras_url="https://github.com/oras-project/oras/releases/download/v$ORAS_VERSION"
oras_asset="oras_${ORAS_VERSION}_linux_${arch}.tar.gz"
fetch "$oras_url/$oras_asset" "$work/$oras_asset"
fetch "$oras_url/oras_${ORAS_VERSION}_checksums.txt" "$work/oras.sha256"
verify "$(awk -v f="$oras_asset" '$2 == f {print $1}' "$work/oras.sha256")" "$work/$oras_asset"
# Extract only the executable; never unpack arbitrary archive paths as root.
tar -xOf "$work/$oras_asset" oras > "$stage/tools/oras"
chmod 0755 "$stage/tools/"*
for project in kind kubectl oras; do
  case "$project" in
    kind) license_url="https://raw.githubusercontent.com/kubernetes-sigs/kind/$KIND_VERSION/LICENSE";;
    kubectl) license_url="https://raw.githubusercontent.com/kubernetes/kubernetes/$KUBECTL_VERSION/LICENSE";;
    oras) license_url="https://raw.githubusercontent.com/oras-project/oras/v$ORAS_VERSION/LICENSE";;
  esac
  fetch "$license_url" "$stage/licenses/LICENSE-$project"
done
cp LICENSE "$stage/licenses/LICENSE-astrona"
cp packaging/tools.env "$stage/licenses/tool-versions.txt"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
  -ldflags "-s -w -X main.Version=$tag -X main.PackageFormat=$format" \
  -o "$stage/astrona" ./cmd/astrona
asset="astrona-${tag#v}-linux-$arch.$format"
if [[ "$format" == snap ]]; then
  mkdir -p "$work/snap/payload/bin" "$work/snap/snap"
  cp "$stage/astrona" "$work/snap/payload/bin/astrona"
  cp -R "$stage/tools" "$work/snap/payload/bin/tools"
  cp -R "$stage/licenses" "$work/snap/payload/licenses"
  sed -e "s/VERSION_PLACEHOLDER/${tag#v}/" -e "s/ARCH_PLACEHOLDER/$arch/" \
    packaging/snapcraft.yaml > "$work/snap/snap/snapcraft.yaml"
  if [[ -n "${SNAPCRAFT_CONTAINER_ENGINE:-}" ]]; then
    # Build on the container's Linux filesystem: dpkg extraction and ELF
    # patching must not depend on a macOS/Windows shared mount's semantics.
    "$SNAPCRAFT_CONTAINER_ENGINE" run --rm --entrypoint /bin/sh \
      -v "$work/snap:/input:ro" -v "$work/snap:/output" \
      ghcr.io/canonical/snapcraft:8_core24 -c \
      'mkdir -p /project && cp -R /input/. /project/ && cd /project && exec /bin/run-snapcraft.sh pack --destructive-mode --output /output/astrona.snap'
    cp "$work/snap/astrona.snap" "$output/$asset"
  else
    # Native destructive builds belong on disposable Ubuntu 24.04 runners.
    # Developers should use the container engine option above.
    (cd "$work/snap" && snapcraft --destructive-mode --output "$output/$asset")
  fi
else
  export PACKAGE_ARCH="$arch" PACKAGE_VERSION="${tag#v}"
  (cd "$stage" && nfpm package --config "$root/packaging/nfpm.yaml" --packager "$format" --target "$output/$asset")
fi
echo "Built $output/$asset"
