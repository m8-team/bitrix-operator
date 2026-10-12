#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
kustomize=${KUSTOMIZE:-"$PWD/bin/kustomize"}
mode=${1:-render}
case "$mode" in render|server) ;; *) echo 'Usage: test-install-examples.sh [render|server]' >&2; exit 1 ;; esac
if [[ "$mode" == server ]]; then
    # This helper is invoked by e2e-chart.sh only after it creates its isolated cluster.
    : "${KUBECONFIG:?An explicit isolated test kubeconfig is required}"
    kubectl get namespace bitrix >/dev/null
fi
count=0
for directory in config/examples/install/*; do
    [[ "${directory##*/}" == base ]] && continue
    [[ -f "$directory/kustomization.yaml" ]] || continue
    if [[ "$mode" == server ]]; then
        "$kustomize" build "$directory" | kubectl apply --dry-run=server --validate=strict -f - >/dev/null
    else
        "$kustomize" build "$directory" >/dev/null
    fi
    echo "PASS $mode $directory"
    count=$((count + 1))
done
[[ "$count" -ge 7 ]]
