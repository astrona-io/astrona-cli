#!/usr/bin/env bash
# Source this helper in CLI regression tests. The pinned ID must be reviewed
# when settings definitions change; never derive approval from runtime output.
prepare_test_settings() {
  "$@" settings set verbose false
  "$@" settings set keep-context false
  "$@" settings set parallel 1
  "$@" settings set update-check false
  "$@" settings view
  "$@" settings review --accept '7bdd50635bcf87b7df6b4dc6d5d54253d23011057cfb4e9c2ea84a129172915c'
}
