#!/usr/bin/env bash
set -euo pipefail
# Proves astrona can run a command inside the VM over SSH. It deliberately
# leaves /tmp/astrona-lab alone: creating it and the marker file is the
# student's task, and this script runs as the admin user `astrona`, so
# anything it created there would belong to `astrona`, not `student`.
echo "hello from inside the qemu VM: $(hostname)"
