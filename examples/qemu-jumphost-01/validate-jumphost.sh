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

# sshAccess wired an ed25519 identity + ~/.ssh/config Host aliases into
# jumphost at boot (see config.yaml) — `ssh client`/`ssh server` should need
# no password and no manual key setup at all.
#
# </dev/null on both is required, not decoration: this script itself runs
# on jumphost with its content piped over stdin to `bash -s` (astrona's
# SSHExecutor — see executor.go), so an inner `ssh` left free to inherit
# that same stdin will read and discard however much of *this script* is
# still unread at that point, silently truncating everything after it. The
# symptom is exactly that: no error, no failing exit code, just later lines
# in this file never running.
if ! ssh -o BatchMode=yes -o ConnectTimeout=5 client hostname </dev/null >/dev/null 2>&1; then
  echo "expected 'ssh client' from jumphost to succeed passwordlessly via sshAccess, but it didn't"
  exit 1
fi
echo "jumphost -> client: passwordless ssh via sshAccess, as expected"

if ! ssh -o BatchMode=yes -o ConnectTimeout=5 server hostname </dev/null >/dev/null 2>&1; then
  echo "expected 'ssh server' from jumphost to succeed passwordlessly via sshAccess, but it didn't"
  exit 1
fi
echo "jumphost -> server: passwordless ssh via sshAccess, as expected"
