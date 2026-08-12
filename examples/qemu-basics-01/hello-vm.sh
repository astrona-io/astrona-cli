#!/usr/bin/env bash
set -euo pipefail
echo "hello from inside the qemu VM: $(hostname)"
mkdir -p /tmp/astrona-lab
echo "qemu-smoke-ok" > /tmp/astrona-lab/marker.txt
