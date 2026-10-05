# Sourced by the isolated kind test after the cron workload has been disabled.
# Reuse its single-node RWX fixture; this does not certify distributed storage.
(
set -Eeuo pipefail
tmp_hpa=$(mktemp -d)
load_pid=''
cleanup_hpa() {
    if [ -n "$load_pid" ]; then kill "$load_pid" 2>/dev/null || true; fi
    rm -rf "$tmp_hpa"
}
trap cleanup_hpa EXIT
wait_hpa_cluster() {
    kubectl -n bitrix-web-test wait bitrixcluster/cron --for=jsonpath='{.status.observedGeneration}'="$(kubectl -n bitrix-web-test get bitrixcluster/cron -o jsonpath='{.metadata.generation}')" --timeout=120s
    kubectl -n bitrix-web-test wait bitrixcluster/cron --for=condition=Ready --timeout=240s
}
echo 'Checking HPA without metrics-server'
kubectl -n bitrix-web-test patch bitrixcluster/cron --type=merge -p '{"spec":{"runtime":{"web":{"replicas":1,"autoscaling":{"enabled":true,"minReplicas":1,"maxReplicas":3,"targetCPUUtilization":50},"nginx":{"resources":{"requests":{"cpu":"100m","memory":"64Mi"}}},"php":{"resources":{"requests":{"cpu":"100m","memory":"64Mi"}}}}}}'
kubectl -n bitrix-web-test wait bitrixcluster/cron --for=jsonpath='{.status.conditions[?(@.type=="AutoscalingReady")].reason}'=MetricsPending --timeout=180s
kubectl -n bitrix-web-test wait bitrixcluster/cron --for=condition=WebReady --timeout=240s
# Pin release bytes. Insecure kubelet TLS is ONLY for this disposable kind fixture.
curl -fsSL --retry 3 -o "$tmp_hpa/metrics.yaml" https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.9.0/components.yaml
echo "1cec29a5267809306a2c6ec74a3e449abbb705b4a8beed0c8a1963910f72c79b  $tmp_hpa/metrics.yaml" | sha256sum -c -
kubectl apply -f "$tmp_hpa/metrics.yaml"
kubectl -n kube-system patch deployment/metrics-server --type=json -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
kubectl -n kube-system rollout status deployment/metrics-server --timeout=180s
kubectl wait apiservice/v1beta1.metrics.k8s.io --for=condition=Available --timeout=180s
wait_hpa_cluster
echo 'Checking real CPU-driven scale-out'
uid=$(kubectl -n bitrix-web-test get bitrixcluster/cron -o jsonpath='{.metadata.uid}')
load_pod=$(kubectl -n bitrix-web-test get pod -l "bitrix.m8.team/owner-uid=$uid,app.kubernetes.io/name=bitrix" -o jsonpath='{.items[0].metadata.name}')
# CPU work is bounded and runs only in the synthetic application's PHP container.
kubectl -n bitrix-web-test exec "$load_pod" -c php -- touch /tmp/hpa-load
kubectl -n bitrix-web-test exec "$load_pod" -c php -- php -r '$end=microtime(true)+240; while (microtime(true)<$end && file_exists("/tmp/hpa-load")) { for($i=0;$i<10000;$i++){hash("sha256",str_repeat("a",4096));} clearstatcache(); }' &
load_pid=$!
kubectl -n bitrix-web-test wait deployment/cron-web --for=jsonpath='{.spec.replicas}'=3 --timeout=180s
kubectl -n bitrix-web-test rollout status deployment/cron-web --timeout=180s
wait_hpa_cluster
test "$(kubectl -n bitrix-web-test get bitrixcluster/cron -o jsonpath='{.status.runtime.web.desiredReplicas}')" = 3
kubectl -n bitrix-operator-system rollout restart deployment/bitrix-operator-controller-manager
kubectl -n bitrix-operator-system rollout status deployment/bitrix-operator-controller-manager --timeout=120s
wait_hpa_cluster
test "$(kubectl -n bitrix-web-test get deployment/cron-web -o jsonpath='{.spec.replicas}')" = 3
kubectl -n bitrix-web-test exec "$load_pod" -c php -- rm /tmp/hpa-load
wait "$load_pid"
load_pid=''
echo 'Checking lower HPA bounds and fixed-replica restoration'
# Reduce the bound to exercise real scale-in without waiting the default 300s window.
kubectl -n bitrix-web-test patch bitrixcluster/cron --type=merge -p '{"spec":{"runtime":{"web":{"autoscaling":{"maxReplicas":1}}}}}'
kubectl -n bitrix-web-test wait deployment/cron-web --for=jsonpath='{.spec.replicas}'=1 --timeout=120s
wait_hpa_cluster
kubectl -n bitrix-web-test patch bitrixcluster/cron --type=merge -p '{"spec":{"runtime":{"web":{"replicas":2,"autoscaling":{"enabled":false}}}}}'
kubectl -n bitrix-web-test wait hpa/cron-web --for=delete --timeout=120s
kubectl -n bitrix-web-test wait deployment/cron-web --for=jsonpath='{.spec.replicas}'=2 --timeout=120s
wait_hpa_cluster
echo 'HPA E2E passed: missing metrics, CPU scale-out, restart, scale-in and fixed replicas'
)
