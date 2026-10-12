#!/usr/bin/env bash
# Source this helper in CLI regression tests. The pinned ID must be reviewed
# when settings definitions change; never derive approval from runtime output.
prepare_test_settings() {
  "$@" settings set verbose false
  "$@" settings set keep-context false
  "$@" settings set parallel 1
  "$@" settings set update-check false
  "$@" settings set cache-retention-days 30
  "$@" settings set cache-max-size-gb 50
  "$@" settings view
  "$@" settings review --accept '5dcd01779335122e1e33b46c00b2d1fb5492958346f5e7e1f07eca923d9cab7c'
}
