#!/usr/bin/env bash
# Tests only in a new, disposable kind cluster and never uses the caller's kubeconfig.
set -Eeuo pipefail
cd "$(dirname "$0")/.."
for command in kind docker kubectl python3; do command -v "$command" >/dev/null; done
cluster="bx-chart-$(date +%s)-$$"
if kind get clusters 2>/dev/null | grep -qx "$cluster"; then
    echo "Refusing to use existing cluster $cluster" >&2
    exit 1
fi
chart_tmp=$(mktemp -d)
export KUBECONFIG="$chart_tmp/kubeconfig"
created=false
cleanup() {
    result=$?
    if [ "$created" = true ]; then
        if [ "$result" -ne 0 ]; then
            kubectl get pods,bitrixclusters,pvc -A -o wide || true
            kubectl -n bitrix-chart-system get events --sort-by=.lastTimestamp || true
            kubectl -n bitrix-chart-system logs deployment/bx-chart-controller-manager --tail=60 || true
        fi
        kind delete cluster --name "$cluster" || true
    fi
    rm -rf -- "$chart_tmp"
    exit "$result"
}
trap cleanup EXIT
trap 'echo "Install artifacts E2E failed at line $LINENO" >&2' ERR
created=true
kind create cluster --name "$cluster" --kubeconfig "$KUBECONFIG" \
    --image kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5 --wait 120s
docker build -t bitrix-operator:e2e .
kind load docker-image bitrix-operator:e2e --name "$cluster"
make build-installer IMG=bitrix-operator:e2e
git diff --exit-code -- config
kubectl apply -f dist/install.yaml
kubectl -n bitrix-operator-system rollout status deployment/bitrix-operator-controller-manager --timeout=180s
# Only here: the cluster is empty of user CRs/PVCs. Never use this as a production uninstall command.
kubectl delete -f dist/install.yaml --ignore-not-found=true --wait=true
kubectl wait crd/bitrixclusters.bitrix.m8.team --for=delete --timeout=120s
echo 'PASS Kustomize installer in a clean cluster'
bin/helm package dist/chart --destination "$chart_tmp"
chart="$chart_tmp/bitrix-operator-0.1.0-dev.tgz"
helm_args=(--namespace bitrix-chart-system --set-string fullnameOverride=bx-chart \
    --set-string manager.image.reference=bitrix-operator:e2e --set manager.image.pullPolicy=Never --wait --timeout 180s)
bin/helm install bitrix-operator "$chart" --create-namespace "${helm_args[@]}"
kubectl create namespace bitrix-chart-data
kubectl create --dry-run=client -f config/samples/bitrix_v1alpha1_bitrixcluster.yaml -o json |
    python3 -c 'import json,sys; c=json.load(sys.stdin); c["metadata"]={"name":"retained","namespace":"bitrix-chart-data"}; json.dump(c,sys.stdout)' |
    kubectl create -f -
kubectl -n bitrix-chart-data wait bitrixcluster/retained \
    --for=jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'=SecretMissing --timeout=120s
# An unbound data claim tests object retention, not CSI persistence (covered separately).
kubectl create -f test/e2e/chart/retained-pvc.yaml
crd_uid=$(kubectl get crd/bitrixclusters.bitrix.m8.team -o jsonpath='{.metadata.uid}')
cr_uid=$(kubectl -n bitrix-chart-data get bitrixcluster/retained -o jsonpath='{.metadata.uid}')
pvc_uid=$(kubectl -n bitrix-chart-data get pvc/retained-data -o jsonpath='{.metadata.uid}')
bin/helm upgrade bitrix-operator "$chart" "${helm_args[@]}" --set manager.replicas=2
test "$(kubectl -n bitrix-chart-system get deployment/bx-chart-controller-manager -o jsonpath='{.status.readyReplicas}')" = 2
bin/helm rollback bitrix-operator 1 --namespace bitrix-chart-system --wait --timeout 180s
test "$(kubectl -n bitrix-chart-system get deployment/bx-chart-controller-manager -o jsonpath='{.spec.replicas}')" = 1
bin/helm uninstall bitrix-operator --namespace bitrix-chart-system --wait --timeout 180s
test "$(kubectl get crd/bitrixclusters.bitrix.m8.team -o jsonpath='{.metadata.uid}')" = "$crd_uid"
test "$(kubectl -n bitrix-chart-data get bitrixcluster/retained -o jsonpath='{.metadata.uid}')" = "$cr_uid"
test "$(kubectl -n bitrix-chart-data get pvc/retained-data -o jsonpath='{.metadata.uid}')" = "$pvc_uid"
bin/helm install bitrix-operator "$chart" "${helm_args[@]}"
test "$(kubectl get crd/bitrixclusters.bitrix.m8.team -o jsonpath='{.metadata.uid}')" = "$crd_uid"
kubectl -n bitrix-chart-data wait bitrixcluster/retained \
    --for=jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'=SecretMissing --timeout=120s
echo 'Install artifacts E2E passed: bundle, Helm install/upgrade/rollback/uninstall/reinstall, retained CRD/CR/PVC'
