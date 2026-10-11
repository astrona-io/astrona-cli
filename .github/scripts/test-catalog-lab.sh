#!/usr/bin/env bash
# Run on Ubuntu with Docker or Podman, Kind and kubectl. No login required.
set -euo pipefail

astrona="$(realpath "${1:-./astrona}")"
lab=ATS000/section-010/module-01/lab-01
cluster=astro-ats-000-lab-010-01
artifacts="$(realpath -m "${2:-catalog-e2e-diagnostics}")"
mkdir -p "$artifacts"
# Isolate credentials, lab state and kubectl contexts from the caller.
export HOME
HOME="$(mktemp -d)"
export KUBECONFIG="$HOME/kubeconfig"
engine="${3:-docker}"
case "$engine" in
  docker|podman) ;;
  *) echo "Expected container engine docker or podman" >&2; exit 1 ;;
esac
export KIND_EXPERIMENTAL_PROVIDER="$engine"
unset ASTRONA_URL
started=false

cleanup() {
  result=$?
  trap - EXIT
  if (( result != 0 )) && [[ "$started" == true ]]; then
    kind export logs "$artifacts/kind" --name "$cluster" || true
    kubectl --context "kind-$cluster" get pods -A -o wide > "$artifacts/pods.txt" 2>&1 || true
    kubectl --context "kind-$cluster" describe pods -n first-lab > "$artifacts/describe.txt" 2>&1 || true
    cp -R "$HOME/.astrona" "$artifacts/astrona" 2>/dev/null || true
    # Only this test's cluster is eligible for failure cleanup.
    "$astrona" destroy "$cluster" --verbose || true
    kind delete cluster --name "$cluster" || true
  fi
  exit "$result"
}
trap cleanup EXIT

# Fail rather than replace a cluster left by another run.
clusters="$(kind get clusters)"
if grep -Fxq "$cluster" <<< "$clusters"; then
  echo "Test cluster already exists: $cluster" >&2
  exit 1
fi

test ! -e "$HOME/.astrona/credentials.json"
started=true
# Headless runs must explicitly accept the lack of account tracking.
if "$astrona" run "$lab" --trust --verbose < /dev/null > "$artifacts/unconfirmed.log" 2>&1; then
  echo "Signed-out run proceeded without confirmation" >&2
  exit 1
fi
grep -F -- '--without-login' "$artifacts/unconfirmed.log"
clusters="$(kind get clusters)"
if grep -Fxq "$cluster" <<< "$clusters"; then
  echo "Unconfirmed run created a cluster" >&2
  exit 1
fi
"$astrona" run "$lab" --without-login --trust --verbose 2>&1 | tee "$artifacts/run.log"
clusters="$(kind get clusters)"
grep -Fx "$cluster" <<< "$clusters"
kubectl --context "kind-$cluster" wait --for=condition=Ready nodes --all --timeout=120s
kubectl --context "kind-$cluster" -n first-lab get deployment hello

# The first lab intentionally has a bad image tag. Repair it and forbid
# pulls: Ready replicas now prove the Alpine image was loaded into Kind.
kubectl --context "kind-$cluster" -n first-lab patch deployment hello --type=strategic \
  -p '{"spec":{"template":{"spec":{"containers":[{"name":"web","image":"nginx:1.27-alpine","imagePullPolicy":"Never"}]}}}}'
kubectl --context "kind-$cluster" -n first-lab rollout status deployment/hello --timeout=180s
test "$(kubectl --context "kind-$cluster" -n first-lab get deployment hello -o jsonpath='{.status.readyReplicas}')" = 2

"$astrona" destroy "$lab" --verbose 2>&1 | tee "$artifacts/destroy.log"
clusters="$(kind get clusters)"
if grep -Fxq "$cluster" <<< "$clusters"; then
  echo "Destroy left the Kind cluster running" >&2
  exit 1
fi
containers="$("$engine" ps -aq --filter "label=io.x-k8s.kind.cluster=$cluster")"
test -z "$containers"
contexts="$(kubectl config get-contexts -o name)"
if grep -Fxq "kind-$cluster" <<< "$contexts"; then
  echo "Destroy left the lab's kubeconfig context" >&2
  exit 1
fi
test ! -e "$HOME/.astrona/labs/$cluster.json"
test ! -e "$HOME/.astrona/credentials.json"
echo "PASS: anonymous ATS000 lab ready, Alpine preload verified, destroy verified."
