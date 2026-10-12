#!/usr/bin/env bash
# Run inside a disposable distro container (root), or on a Snap CI runner.
set -euo pipefail
format="${1:?usage: test-linux-package.sh <deb|rpm|snap> <package> <tag>}"
package="$(realpath "${2:?missing package}")"
tag="${3:?missing version}"
install_package() {
  case "$format" in
    deb) apt-get update; apt-get install -y "$package";;
    rpm) dnf install -y "$package";;
    snap) sudo snap install --dangerous --classic "$package";;
    *) echo 'Unsupported package format' >&2; exit 1;;
  esac
}
install_package
export ASTRONA_NO_UPDATE_CHECK=1
if [[ "$format" == snap ]]; then
  astrona=/snap/bin/astrona
  tools="$(readlink -f /snap/astrona/current)/bin/tools"
  executable="${tools%/tools}/astrona"
else
  astrona=/usr/bin/astrona
  tools=/usr/lib/astrona/tools
  executable=/usr/lib/astrona/astrona
fi
"$astrona" --version | grep -F "$tag"
source "$(dirname "${BASH_SOURCE[0]}")/review-test-settings.sh"
prepare_test_settings "$astrona"
"$tools/kind" version
"$tools/kubectl" version --client
"$tools/oras" version
"$astrona" completion bash > /tmp/astrona-completion.bash
test -s /tmp/astrona-completion.bash
# A missing engine is expected here. Verify CLI tool lookup, including
# Git supplied by package dependencies, without installing tools globally.
"$astrona" check --accept-terms > /tmp/astrona-package-check.log 2>&1 || true
cat /tmp/astrona-package-check.log
grep -F "$tools/kind" /tmp/astrona-package-check.log
grep -F "$tools/kubectl" /tmp/astrona-package-check.log
grep -F "$tools/oras" /tmp/astrona-package-check.log
grep -E '✓.*git ' /tmp/astrona-package-check.log
# Validate a known example through Git over HTTPS. This does not execute
# lab scripts or create a cluster, and catches broken Git helpers/CA bundles.
"$astrona" validate --accept-terms --git https://github.com/astrona-io/astrona-cli.git \
  --git-ref "${PACKAGE_GIT_REF:-main}" -c examples/k8s-basics-01
# Even --force must never download a raw binary into a package-owned file.
before="$(sha256sum "$executable")"
for flag in '' --force; do
  if "$astrona" upgrade --accept-terms $flag > /tmp/astrona-package-upgrade.log 2>&1; then
    echo 'Package upgrade should return installation instructions' >&2; exit 1
  fi
  grep -F 'managed by a' /tmp/astrona-package-upgrade.log
done
test "$before" = "$(sha256sum "$executable")"
# Local Snap install is also how a newer local revision is installed.
case "$format" in
  deb) apt-get install --reinstall -y "$package";;
  rpm) dnf reinstall -y "$package";;
  snap) install_package;;
esac
"$astrona" --version | grep -F "$tag"
mkdir -p "$HOME/.astrona"
printf 'keep package user data\n' > "$HOME/.astrona/package-test-marker"
case "$format" in
  deb) apt-get remove -y astrona;;
  rpm) dnf remove -y astrona;;
  snap) sudo snap remove astrona;;
esac
test ! -e "$astrona"
test ! -e "$tools"
grep -F 'keep package user data' "$HOME/.astrona/package-test-marker"
rm "$HOME/.astrona/package-test-marker"
echo "$format installation, tool discovery, reinstall and removal passed"
