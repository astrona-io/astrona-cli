#!/bin/bash
# Reference solution: a Job in the lab's own cluster that logs in against
# the linked idp cluster. The host comes from astrona — never hard-coded.
set -euo pipefail
kubectl create job login --image=curlimages/curl:8.11.1 -- \
  curl -fsS "http://${ASTRONA_LINK_IDP_HOST}:30080/token"
kubectl wait job/login --for=condition=Complete --timeout=120s
