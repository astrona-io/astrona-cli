#!/usr/bin/env bash
set -euo pipefail
CONTENT=$(cat /tmp/astrona-lab/marker.txt)
if [ "$CONTENT" != "qemu-smoke-ok" ]; then
  echo "expected /tmp/astrona-lab/marker.txt to contain 'qemu-smoke-ok', got '$CONTENT'"
  exit 1
fi
echo "qemu SSH exec + file write verified inside the VM"
