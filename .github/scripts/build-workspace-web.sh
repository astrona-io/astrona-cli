#!/usr/bin/env bash
# Build the browser workspace frontend into internal/workspace/web/dist, where
# go:embed picks it up, and fail if the committed placeholder page is still
# what would be embedded. Release builds run this before `go build`.
# Requires Node (version in internal/workspace/web/.nvmrc) and npm.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
web="$root/internal/workspace/web"
cd "$web"
npm ci --no-audit --no-fund
npm run build
"$root/.github/scripts/check-workspace-web.sh"
