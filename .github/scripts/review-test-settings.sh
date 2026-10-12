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
  "$@" settings review --accept '069734490af71b2f96094068b08073014a7426b021862c8fb4b66b72aac00bd6'
}
