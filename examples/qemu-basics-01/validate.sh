#!/usr/bin/env bash
set -euo pipefail
MARKER=/tmp/astrona-lab/marker.txt
if [ ! -f "$MARKER" ]; then
  echo "expected a file at $MARKER, but it does not exist"
  exit 1
fi
CONTENT=$(cat "$MARKER")
if [ "$CONTENT" != "qemu-smoke-ok" ]; then
  echo "expected $MARKER to contain 'qemu-smoke-ok', got '$CONTENT'"
  exit 1
fi
echo "qemu SSH exec + file write verified inside the VM"
