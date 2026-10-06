#!/usr/bin/env bash
# Checks the Homebrew formula homebrew-formula.sh generates, against a
# locally built astrona — what the tap would ship, before a release:
#
#   1. renders the formula for a fake release (vX.Y.Z, SHA256SUMS of the
#      binary built here) and runs `brew style` and `brew audit --strict` on it;
#   2. installs a copy of it pointed at the local binary (file:// URL) and
#      runs `brew test`.
#
# The installed copy is named astrona-formula-ci and is keg-only, so it never
# replaces or shadows an astrona already installed with Homebrew. The
# temporary tap is removed on exit. Needs: brew, go, a macOS or Linux host.
set -euo pipefail

version="0.0.1"
tag="v${version}"
tap="astrona-ci/formula-test"
root="$(cd "$(dirname "$0")/../.." && pwd)"
work="$(mktemp -d)"

export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1 HOMEBREW_NO_ENV_HINTS=1 HOMEBREW_NO_INSTALL_CLEANUP=1

cleanup() {
  brew uninstall --force --quiet "${tap}/astrona-formula-ci" >/dev/null 2>&1 || true
  brew untap --force --quiet "$tap" >/dev/null 2>&1 || true
  rm -rf "$work"
}
trap cleanup EXIT

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
esac
asset="astrona-${os}-${arch}"

echo "==> Building ${asset} (${tag})"
mkdir -p "$work/dist"
(cd "$root" && go build -ldflags "-X main.Version=${tag}" -o "$work/dist/${asset}" ./cmd/astrona)

# The other three platforms only need well-formed checksums here.
for a in astrona-darwin-arm64 astrona-darwin-amd64 astrona-linux-arm64 astrona-linux-amd64; do
  if [[ "$a" == "$asset" ]]; then
    (cd "$work/dist" && shasum -a 256 "$a")
  else
    echo "$(printf '0%.0s' {1..64})  $a"
  fi
done > "$work/dist/SHA256SUMS"

"$root/.github/scripts/homebrew-formula.sh" "$tag" "$work/dist/SHA256SUMS" > "$work/astrona.rb"

brew tap-new --no-git "$tap" >/dev/null
tapdir="$(brew --repository "$tap")"

echo "==> brew style / brew audit --strict"
cp "$work/astrona.rb" "$tapdir/Formula/astrona.rb"
brew style "${tap}/astrona"
brew audit --strict --formula "${tap}/astrona"
rm "$tapdir/Formula/astrona.rb"

echo "==> brew install + brew test (local binary)"
url="https://github.com/astrona-io/astrona-cli/releases/download/${tag}/${asset}"
# perl, not sed: BSD sed (macOS) doesn't turn \n into a newline.
URL="$url" LOCAL="file://${work}/dist/${asset}" VERSION="$version" perl -pe '
  s/\Q$ENV{URL}\E/$ENV{LOCAL}/;
  s/^class Astrona < Formula/class AstronaFormulaCi < Formula/;
  s/^  license "Apache-2.0"/  version "$ENV{VERSION}"\n  license "Apache-2.0"/;
  s/^  depends_on "kind"/  keg_only "formula test copy, must not shadow a real astrona"\n\n  depends_on "kind"/;
' "$work/astrona.rb" > "$tapdir/Formula/astrona-formula-ci.rb"
grep -q "file://${work}/dist/${asset}" "$tapdir/Formula/astrona-formula-ci.rb"

brew install "${tap}/astrona-formula-ci"
brew test "${tap}/astrona-formula-ci"

prefix="$(brew --prefix "${tap}/astrona-formula-ci")"
"$prefix/bin/astrona" --version | grep -q "$tag"
test -s "$prefix/share/man/man1/astrona-run.1"
test -s "$prefix/share/zsh/site-functions/_astrona"
test -s "$prefix/share/fish/vendor_completions.d/astrona.fish"
echo "==> Formula OK"
