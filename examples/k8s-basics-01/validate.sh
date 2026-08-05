#!/usr/bin/env bash
set -euo pipefail

VALUE=$(kubectl get configmap hello-config -n lab-ns -o jsonpath='{.data.message}')

if [ "$VALUE" != "hello world" ]; then
  echo "expected hello-config data.message to be 'hello world', got '$VALUE'"
  exit 1
fi

echo "hello-config data.message content verified"
