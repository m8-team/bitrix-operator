#!/usr/bin/env bash
# Destructive ONLY inside a newly created, disposable kind cluster.
set -Eeuo pipefail
cd "$(dirname "$0")/.."
for command in kind docker kubectl python3; do command -v "$command" >/dev/null; done
cluster="bx-restore-$(date +%s)-$$"
if kind get clusters 2>/dev/null | grep -qx "$cluster"; then
    echo "Refusing to use existing cluster $cluster" >&2
    exit 1
fi
umask 077
recovery_tmp=$(mktemp -d)
export KUBECONFIG="$recovery_tmp/kubeconfig"
created=false
cleanup() {
    result=$?
    if [ "$created" = true ]; then
        if [ "$result" -ne 0 ]; then
            kubectl get pods,pvc,bitrixclusters -A -o wide || true
            kubectl -n bitrix-recovery-test get events --sort-by=.lastTimestamp || true
            kubectl -n bitrix-operator-system logs deployment/bitrix-operator-controller-manager -c manager --tail=60 || true
        fi
        kind delete cluster --name "$cluster" || true
    fi
    # Includes synthetic credentials and backups: never publish these as CI artifacts.
    rm -rf -- "$recovery_tmp"
    exit "$result"
}
trap cleanup EXIT
trap 'echo "Recovery E2E failed at line $LINENO" >&2' ERR
python3 -m unittest discover -s test/e2e/recovery -p 'test_*.py'
created=true
kind create cluster --name "$cluster" --kubeconfig "$KUBECONFIG" \
    --image kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5 --wait 120s
docker build -t bitrix-operator:e2e .
docker build -f test/fixtures/runtime/Dockerfile -t bitrix-runtime-fixture:e2e .
kind load docker-image bitrix-operator:e2e bitrix-runtime-fixture:e2e --name "$cluster"
make install deploy IMG=bitrix-operator:e2e
kubectl -n bitrix-operator-system rollout status deployment/bitrix-operator-controller-manager --timeout=180s
kubectl apply -f test/e2e/web/dependencies.yaml
kubectl -n bitrix-dependencies-test rollout status deployment/dependencies --timeout=300s
for pair in bitrix-license:license bitrix-crypto:key bitrix-database:password bitrix-redis:password; do
    kubectl -n bitrix-web-test create secret generic "${pair%:*}" --from-literal="${pair#*:}=fixture-password"
done
for engine in MySQL PostgreSQL; do
    kubectl create --dry-run=client -f config/samples/bitrix_v1alpha1_bitrixcluster.yaml -o json |
        python3 test/e2e/web/cluster.py "$engine" | kubectl apply -f -
    kubectl -n bitrix-web-test wait "bitrixcluster/${engine,,}" --for=condition=Ready --timeout=300s
done
python3 test/e2e/recovery/drill.py "$cluster" "$recovery_tmp"
