#!/usr/bin/env bash
# Fail when internal/workspace/web/dist holds the placeholder page instead of
# the built frontend. A release binary built from it would serve "frontend not
# built" to every learner. Run right before `go build` in release jobs.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
dist="$root/internal/workspace/web/dist"
if [[ ! -f "$dist/index.html" ]]; then
  echo "workspace frontend: $dist/index.html is missing" >&2
  exit 1
fi
if grep -q 'name="astrona-workspace-placeholder"' "$dist/index.html"; then
  echo "workspace frontend: dist/index.html is the placeholder; run .github/scripts/build-workspace-web.sh first" >&2
  exit 1
fi
if ! compgen -G "$dist/assets/*.js" >/dev/null; then
  echo "workspace frontend: no JavaScript in $dist/assets" >&2
  exit 1
fi
if [[ ! -f "$dist/THIRD_PARTY_NOTICES.txt" ]]; then
  echo "workspace frontend: THIRD_PARTY_NOTICES.txt is missing" >&2
  exit 1
fi
echo "workspace frontend: built assets present"
