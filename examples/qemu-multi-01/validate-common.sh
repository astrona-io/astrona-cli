#!/usr/bin/env bash
set -euo pipefail
CONTENT=$(cat /tmp/astrona-lab/common-marker.txt)
if [ "$CONTENT" != "common-ready" ]; then
  echo "expected /tmp/astrona-lab/common-marker.txt to contain 'common-ready', got '$CONTENT'"
  exit 1
fi
echo "common bootstrap verified on $(hostname)"
