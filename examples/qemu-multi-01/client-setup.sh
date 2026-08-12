#!/usr/bin/env bash
set -euo pipefail
echo "hello from the client VM: $(hostname)"
mkdir -p /tmp/astrona-lab
echo "client-ready" > /tmp/astrona-lab/client-marker.txt
