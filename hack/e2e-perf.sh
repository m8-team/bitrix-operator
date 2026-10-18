#!/usr/bin/env bash
# Refuse existing kind cluster and use an isolated temporary kubeconfig.
set -Eeuo pipefail
cd "$(dirname "$0")/.."
cluster=bitrix-perf-ci
if kind get clusters 2>/dev/null | grep -qx "$cluster"; then
    echo "Refusing to reuse existing kind cluster: $cluster" >&2
    exit 1
fi
kubeconfig=$(mktemp)
trap 'kind delete cluster --name "$cluster" >/dev/null 2>&1 || true; rm -f "$kubeconfig"' EXIT
export KUBECONFIG="$kubeconfig"
kind create cluster --name "$cluster" --kubeconfig "$KUBECONFIG" \
    --image kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5 --wait 120s
kubectl --context "kind-$cluster" taint node "$cluster-control-plane" node-role.kubernetes.io/control-plane:NoSchedule- || true
docker build -t bitrix-operator:perf .
kind load docker-image bitrix-operator:perf --name "$cluster"
make install deploy IMG=bitrix-operator:perf
kubectl --context "kind-$cluster" -n bitrix-operator-system \
    set resources deployment/bitrix-operator-controller-manager \
    --containers=manager --requests=cpu=250m,memory=256Mi --limits=cpu=2,memory=2Gi
kubectl --context "kind-$cluster" -n bitrix-operator-system \
    rollout status deployment/bitrix-operator-controller-manager --timeout=180s
python3 test/performance/reconcile_load.py --context "kind-$cluster" \
    --sizes 10 100 1000 --output perf-results.json --timeout 300 --poll 1
