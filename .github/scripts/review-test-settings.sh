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
  "$@" settings review --accept 'd02b689ae5d647c1de39fb2e6c8dcfa6cc81cb497cd015bf29aa2f009b5dbad1'
}
