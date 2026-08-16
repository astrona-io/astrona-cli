#!/usr/bin/env bash
set -euo pipefail
echo "common setup running on: $(hostname)"
echo "interfaces:"
ip -4 -o addr show | awk '{print "  " $2, $4}'
