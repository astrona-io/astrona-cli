#!/usr/bin/env bash
set -euo pipefail

if ! ping -c 1 -W 2 10.20.1.1 >/dev/null 2>&1; then
  echo "expected server to reach jumphost (10.20.1.1) over server-net, but it didn't"
  exit 1
fi
echo "server -> jumphost (server-net): reachable, as expected"

if ping -c 1 -W 2 10.10.1.10 >/dev/null 2>&1; then
  echo "server reached client (10.10.1.10) directly — it shouldn't be able to, they don't share a NIC"
  exit 1
fi
echo "server -> client (no shared segment): unreachable, as expected"
