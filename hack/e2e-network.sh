#!/usr/bin/env bash
# Always owns a new cluster and private kubeconfig; never uses an existing cluster.
set -Eeuo pipefail
cd "$(dirname "$0")/.."
for command in kind docker kubectl cilium go python3; do command -v "$command" >/dev/null; done
cluster="bitrix-network-$(date +%s)-$$"
if kind get clusters 2>/dev/null | grep -qx "$cluster"; then
    echo "Refusing to use existing cluster $cluster" >&2
    exit 1
fi
network_tmp=$(mktemp -d)
export KUBECONFIG="$network_tmp/kubeconfig"
created=false
cleanup() {
    result=$?
    if [ "$created" = true ]; then
        if [ "$result" -ne 0 ]; then
            kubectl get pods -A -o wide || true
            kubectl get networkpolicies -A -o yaml || true
            cilium status --interactive=false || true
            kubectl -n kube-system logs daemonset/cilium --tail=80 || true
        fi
        kind delete cluster --name "$cluster" || true
    fi
    rm -rf -- "$network_tmp"
    exit "$result"
}
trap cleanup EXIT
trap 'echo "Network E2E failed at line $LINENO" >&2' ERR

make kustomize
CGO_ENABLED=0 GOOS=linux go build -trimpath -buildvcs=false -o "$network_tmp/netprobe" ./test/fixtures/network
cp test/fixtures/network/Dockerfile "$network_tmp/Dockerfile"
docker build -t bitrix-network-fixture:e2e "$network_tmp"
# Nodes are expected NotReady before Cilium; do not wait for node readiness here.
created=true
kind create cluster --name "$cluster" --kubeconfig "$KUBECONFIG" \
    --config test/e2e/network/kind.yaml \
    --image kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5
kubectl taint node "$cluster-control-plane" node-role.kubernetes.io/control-plane:NoSchedule-
curl -fsSL --retry 3 https://helm.cilium.io/cilium-1.20.2.tgz -o "$network_tmp/cilium.tgz"
echo "b2afd87b7f75f875f92a14559f14f59b7babbb479d968e3fd625a20bf30ec20e  $network_tmp/cilium.tgz" | sha256sum -c -
tar --no-same-owner -xzf "$network_tmp/cilium.tgz" -C "$network_tmp"
cilium install --chart-directory "$network_tmp/cilium" --version 1.20.2 \
    --values test/e2e/network/cilium-values.yaml --wait --wait-duration 5m
cilium status --wait --wait-duration 5m --interactive=false
kubectl wait nodes --all --for=condition=Ready --timeout=180s
kubectl -n kube-system rollout status deployment/coredns --timeout=180s
kind load docker-image bitrix-network-fixture:e2e --name "$cluster"
for namespace in bitrix-sites bitrix-data gateway-system bitrix-operator-system monitoring other; do
    kubectl create namespace "$namespace"
    kubectl label namespace "$namespace" pod-security.kubernetes.io/enforce=restricted pod-security.kubernetes.io/enforce-version=v1.36
done
kubectl create --dry-run=client -f test/e2e/network/fixture.yaml -o json |
    python3 test/e2e/network/fixture.py "$cluster" | kubectl apply -f -
for namespace in bitrix-sites bitrix-data gateway-system bitrix-operator-system monitoring other; do
    while IFS= read -r deployment; do
        kubectl -n "$namespace" rollout status "$deployment" --timeout=180s
    done < <(kubectl -n "$namespace" get deployments -o name)
done
python3 test/e2e/network/check.py preflight "$cluster"
bin/kustomize build config/security/workloads | kubectl apply -f -
bin/kustomize build config/security/manager | kubectl apply -f -
go run ./test/e2e/network/render | kubectl apply -f -
python3 test/e2e/network/check.py enforce "$cluster"
