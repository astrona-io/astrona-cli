#!/usr/bin/env bash
set -euo pipefail
echo "hello from the server VM: $(hostname)"
mkdir -p /tmp/astrona-lab
echo "server-ready" > /tmp/astrona-lab/server-marker.txt
