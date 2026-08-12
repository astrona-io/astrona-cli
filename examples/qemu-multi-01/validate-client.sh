#!/usr/bin/env bash
set -euo pipefail
CONTENT=$(cat /tmp/astrona-lab/client-marker.txt)
if [ "$CONTENT" != "client-ready" ]; then
  echo "expected /tmp/astrona-lab/client-marker.txt to contain 'client-ready', got '$CONTENT'"
  exit 1
fi
echo "client VM bootstrap verified"
