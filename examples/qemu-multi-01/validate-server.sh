#!/usr/bin/env bash
set -euo pipefail
CONTENT=$(cat /tmp/astrona-lab/server-marker.txt)
if [ "$CONTENT" != "server-ready" ]; then
  echo "expected /tmp/astrona-lab/server-marker.txt to contain 'server-ready', got '$CONTENT'"
  exit 1
fi
echo "server VM bootstrap verified"
