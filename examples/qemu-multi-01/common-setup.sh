#!/usr/bin/env bash
set -euo pipefail
echo "common setup running on: $(hostname)"
mkdir -p /tmp/astrona-lab
echo "common-ready" > /tmp/astrona-lab/common-marker.txt
