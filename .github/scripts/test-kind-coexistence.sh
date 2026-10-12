#!/usr/bin/env bash
# Docker Desktop on macOS, or a Linux/macOS Podman setup for local coverage.
# Usage: bash .github/scripts/test-kind-coexistence.sh ./astrona artifacts [docker|podman]
set -euo pipefail

astrona_dir="$(cd "$(dirname "${1:-./astrona}")" && pwd)"
astrona="$astrona_dir/$(basename "${1:-./astrona}")"
mkdir -p "${2:-kind-coexistence-diagnostics}"
artifact_root="$(cd "${2:-kind-coexistence-diagnostics}" && pwd)"
artifacts="$(mktemp -d "$artifact_root/run.XXXXXX")"
engine="${3:-docker}"
case "$engine" in docker|podman) ;; *) echo 'Expected docker or podman' >&2; exit 1 ;; esac
export KIND_EXPERIMENTAL_PROVIDER="$engine"
export KUBECONFIG="$artifacts/kubeconfig"
state_home="$(mktemp -d)"
# Preserve per-user engine sockets/contexts while isolating Astrona credentials.
# Changing HOME without this breaks Docker Desktop's desktop-linux context.
engine_config="${DOCKER_CONFIG:-$HOME/.docker}"
engine_xdg="${XDG_CONFIG_HOME:-$HOME/.config}"
run_astrona() {
  env HOME="$state_home" DOCKER_CONFIG="$engine_config" XDG_CONFIG_HOME="$engine_xdg" \
    ASTRONA_URL=https://astrona.io "$astrona" --accept-terms "$@"
}

lab=ATS013/section-010/module-01/playground
cluster=astro-ats-013-playground-010-01
fixture1="coexistence-$(date +%s)-$$-one"
fixture2="coexistence-$(date +%s)-$$-two"
fixture1_started=false
fixture2_started=false
lab_started=false

cleanup() {
  result=$?
  trap - EXIT
  if (( result != 0 )); then
    if [[ "$fixture1_started" == true ]]; then kind export logs "$artifacts/$fixture1" --name "$fixture1" || true; fi
    if [[ "$fixture2_started" == true ]]; then kind export logs "$artifacts/$fixture2" --name "$fixture2" || true; fi
    if [[ "$lab_started" == true ]]; then kind export logs "$artifacts/$cluster" --name "$cluster" || true; fi
    mkdir -p "$artifacts/astrona"
    cp -R "$state_home/.astrona/logs" "$state_home/.astrona/diagnostics" "$artifacts/astrona" 2>/dev/null || true
  fi
  if [[ "$lab_started" == true ]]; then
    run_astrona destroy "$cluster" --verbose || true
    kind delete cluster --name "$cluster" || true
  fi
  if [[ "$fixture2_started" == true ]]; then kind delete cluster --name "$fixture2" || result=1; fi
  if [[ "$fixture1_started" == true ]]; then kind delete cluster --name "$fixture1" || result=1; fi
  # Kubeconfig and local lab state contain private keys; don't upload them.
  rm -f "$KUBECONFIG"
  rm -rf "$state_home"
  exit "$result"
}
trap cleanup EXIT

[[ "$(id -u)" != 0 ]] || { echo 'Run as a regular user, without sudo' >&2; exit 1; }
# Optional stronger assertion used by the macOS Docker Desktop workflow.
if [[ "${ASTRONA_REQUIRE_MACOS_STANDARD_USER:-0}" == 1 ]]; then
  [[ "$(uname -s)" == Darwin ]] || { echo 'Expected macOS' >&2; exit 1; }
  if id -Gn | tr ' ' '\n' | grep -Fxq admin; then
    echo 'Use a macOS account outside the admin group for this regression test' >&2
    exit 1
  fi
fi
clusters="$(kind get clusters)"
for name in "$cluster" "$fixture1" "$fixture2"; do
  if grep -Fxq "$name" <<< "$clusters"; then
    echo "Refusing to replace existing cluster: $name" >&2
    exit 1
  fi
done

# Existing fixtures deliberately have no astro- prefix. Include a worker.
cat > "$artifacts/fixture.yaml" <<'YAML'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
  - role: worker
YAML
fixture1_started=true
kind create cluster --name "$fixture1" --config "$artifacts/fixture.yaml" --wait 180s --retain
fixture2_started=true
kind create cluster --name "$fixture2" --wait 180s --retain
kubectl config use-context "kind-$fixture1"
before_containers="$("$engine" ps -q --filter "label=io.x-k8s.kind.cluster=$fixture1" | sort)"
before_containers2="$("$engine" ps -q --filter "label=io.x-k8s.kind.cluster=$fixture2" | sort)"
test -n "$before_containers"
test -n "$before_containers2"

lab_started=true
run_astrona run "$lab" --without-login --trust --verbose < /dev/null 2>&1 | tee "$artifacts/run.log"
kubectl --context "kind-$cluster" get --raw='/readyz?verbose'
kubectl --context "kind-$cluster" wait --for=condition=Ready nodes --all --timeout=180s
kubectl --context "kind-$cluster" -n kube-system rollout status deployment/coredns --timeout=180s
# A Ready deployment alone does not prove DNS actually resolves a service.
kubectl --context "kind-$cluster" run dns-probe --image=docker.io/library/busybox:1.37 \
  --restart=Never --command -- sh -c 'nslookup kubernetes.default.svc.cluster.local'
kubectl --context "kind-$cluster" wait --for=jsonpath='{.status.phase}'=Succeeded pod/dns-probe --timeout=180s
kubectl --context "kind-$cluster" logs dns-probe | tee "$artifacts/dns.log"
test ! -e "$state_home/.astrona/credentials.json"
run_astrona destroy "$lab" --verbose 2>&1 | tee "$artifacts/destroy.log"
clusters="$(kind get clusters)"
if grep -Fxq "$cluster" <<< "$clusters"; then echo 'Destroy left the lab running' >&2; exit 1; fi
test -z "$("$engine" ps -aq --filter "label=io.x-k8s.kind.cluster=$cluster")"
test ! -e "$state_home/.astrona/labs/$cluster.json"
test "$(kubectl config current-context)" = "kind-$fixture1"
for name in "$fixture1" "$fixture2"; do
  kubectl --context "kind-$name" get --raw='/readyz?verbose'
  kubectl --context "kind-$name" wait --for=condition=Ready nodes --all --timeout=120s
  kubectl --context "kind-$name" -n kube-system rollout status deployment/coredns --timeout=120s
done
test "$("$engine" ps -q --filter "label=io.x-k8s.kind.cluster=$fixture1" | sort)" = "$before_containers"
test "$("$engine" ps -q --filter "label=io.x-k8s.kind.cluster=$fixture2" | sort)" = "$before_containers2"
lab_started=false
echo 'PASS: signed-out ICA playground, API/Nodes/CoreDNS/DNS ready, destroy complete, existing clusters preserved.'
