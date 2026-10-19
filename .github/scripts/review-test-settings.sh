#!/usr/bin/env bash
# Source this helper in CLI regression tests to use consistent preferences.
# Settings review is optional; tests need no review receipt or pinned ID.
prepare_test_settings() {
  "$@" settings set verbose false
  "$@" settings set keep-context false
  "$@" settings set parallel 1
  "$@" settings set update-check false
  "$@" settings set cache-retention-days 30
  "$@" settings set cache-max-size-gb 50
}
