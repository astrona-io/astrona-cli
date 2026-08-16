#!/usr/bin/env bash
set -euo pipefail

if ! ping -c 1 -W 2 10.10.1.10 >/dev/null 2>&1; then
  echo "expected jumphost to reach client (10.10.1.10) over client-net, but it didn't"
  exit 1
fi
echo "jumphost -> client (client-net): reachable, as expected"

if ! ping -c 1 -W 2 10.20.1.10 >/dev/null 2>&1; then
  echo "expected jumphost to reach server (10.20.1.10) over server-net, but it didn't"
  exit 1
fi
echo "jumphost -> server (server-net): reachable, as expected"
