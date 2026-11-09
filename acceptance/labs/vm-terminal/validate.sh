#!/usr/bin/env bash
set -euo pipefail
answer="$(cat /tmp/astrona-lab/answer.txt 2>/dev/null || true)"
if [ "$answer" != "solved" ]; then
  echo "expected /tmp/astrona-lab/answer.txt to contain 'solved', got '$answer'"
  exit 1
fi
echo "answer file found"
