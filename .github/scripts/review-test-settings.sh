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
  "$@" settings review --accept '0c4c167f1ccf46e0f52e0b418f8ab893d71f5636a5867f42ce67093782ee0484'
}
