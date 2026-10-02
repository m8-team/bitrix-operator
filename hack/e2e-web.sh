#!/usr/bin/env bash
# This script owns a new, isolated kind cluster and never uses the caller's kubeconfig.
set -Eeuo pipefail
trap 'echo "Web E2E failed at line $LINENO" >&2' ERR
cd "$(dirname "$0")/.."
cluster=bitrix-web-e2e
if kind get clusters 2>/dev/null | grep -qx "$cluster"; then
    echo "Refusing to use an existing cluster: $cluster" >&2
    exit 1
fi
export KUBECONFIG
KUBECONFIG=$(mktemp)
cleanup() {
    result=$?
    if [ "$result" -ne 0 ]; then
        kubectl get pods,pvc,bitrixclusters -A -o wide || true
        kubectl -n bitrix-web-test get events --sort-by=.lastTimestamp || true
        kubectl -n bitrix-operator-system logs deployment/bitrix-operator-controller-manager -c manager --tail=100 || true
        for name in mysql postgresql cron; do
            for container in release nginx php; do
                kubectl -n bitrix-web-test logs "deployment/$name-web" -c "$container" --tail=50 || true
            done
        done
        kubectl -n bitrix-web-test logs pod/postgresql-push-client -c probe --tail=50 || true
        for role in publisher subscriber; do
            kubectl -n bitrix-web-test logs "deployment/postgresql-push-$role" -c "$role" --tail=50 || true
        done
        kubectl -n bitrix-web-test logs deployment/cron-cron -c cron --tail=50 || true
    fi
    kind delete cluster --name "$cluster"
    rm -f "$KUBECONFIG"
    exit "$result"
}
trap cleanup EXIT
kind create cluster --name "$cluster" --kubeconfig "$KUBECONFIG" \
    --image kindest/node:v1.37.0@sha256:a1ed56cfb0e7b93589bdf97c8cd566405a265939e3620fc4f5de89adff580ae5 --wait 120s
# All images are test artifacts, loaded locally without a registry push.
docker build -t bitrix-operator:e2e .
docker build -f test/fixtures/runtime/Dockerfile -t bitrix-runtime-fixture:e2e .
kind load docker-image bitrix-operator:e2e bitrix-runtime-fixture:e2e --name "$cluster"
make install deploy IMG=bitrix-operator:e2e
kubectl -n bitrix-operator-system rollout status deployment/bitrix-operator-controller-manager --timeout=180s
kubectl apply -f test/e2e/web/dependencies.yaml
kubectl -n bitrix-dependencies-test rollout status deployment/dependencies --timeout=240s
for pair in bitrix-license:license bitrix-crypto:key bitrix-database:password bitrix-redis:password; do
    kubectl -n bitrix-web-test create secret generic "${pair%:*}" --from-literal="${pair#*:}=fixture-password"
done
for engine in MySQL PostgreSQL; do
    kubectl create --dry-run=client -f config/samples/bitrix_v1alpha1_bitrixcluster.yaml -o json |
        python3 test/e2e/web/cluster.py "$engine" | kubectl apply -f -
done
for name in mysql postgresql; do
    kubectl -n bitrix-web-test wait "bitrixcluster/$name" --for=condition=WebReady --timeout=300s
    test "$(kubectl -n bitrix-web-test get "bitrixcluster/$name" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = False
    response=$(kubectl -n bitrix-web-test exec "deployment/$name-web" -c nginx -- \
        wget -qO- --header='Host: portal.example.com' "http://$name-web/")
    test "$response" = runtime-fixture
    echo "Checking actual read-only and writable mounts for $name"
    kubectl -n bitrix-web-test exec "deployment/$name-web" -c php -- sh -ec '
        if (echo forbidden >> /opt/www/index.php) 2>/dev/null; then
            echo "Application code is writable" >&2; exit 1
        fi
        echo persisted > /opt/www/upload/persistent.txt
        echo cached > /opt/www/bitrix/cache/check.txt
    '
    kubectl -n bitrix-web-test exec "deployment/$name-web" -c nginx -- sh -ec '
        if (echo forbidden > /opt/www/upload/nginx-denied.txt) 2>/dev/null; then
            echo "nginx can write uploads" >&2; exit 1
        fi
        test ! -e /run/bitrix-secrets
        test ! -e /run/bitrix-config/manifest.json
    '
done
# Secret rotation must change the template and converge to ready replicas.
echo "Checking Secret rotation and data persistence"
old=$(kubectl -n bitrix-web-test get deployment/mysql-web -o jsonpath='{.spec.template.metadata.annotations.bitrix\.m8\.team/secret-revision}')
kubectl -n bitrix-web-test patch secret bitrix-crypto --type=merge -p '{"stringData":{"key":"rotated-fixture-key"}}'
for attempt in $(seq 1 60); do
    new=$(kubectl -n bitrix-web-test get deployment/mysql-web -o jsonpath='{.spec.template.metadata.annotations.bitrix\.m8\.team/secret-revision}')
    [ "$new" != "$old" ] && break
    sleep 2
done
test "$new" != "$old"
kubectl -n bitrix-web-test rollout status deployment/mysql-web --timeout=180s
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=condition=WebReady --timeout=120s
test "$(kubectl -n bitrix-web-test exec deployment/mysql-web -c php -- cat /opt/www/upload/persistent.txt)" = persisted
# Removing a dependency affects readiness, never liveness or process restart counts.
echo "Checking dependency outage and recovery"
kubectl -n bitrix-dependencies-test scale deployment/dependencies --replicas=0
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=condition=WebReady=False --timeout=120s
kubectl -n bitrix-web-test get pods -o json | python3 -c \
    'import json,sys; assert all(s["restartCount"] == 0 for p in json.load(sys.stdin)["items"] for s in p["status"].get("containerStatuses", []))'
kubectl -n bitrix-dependencies-test scale deployment/dependencies --replicas=1
kubectl -n bitrix-dependencies-test rollout status deployment/dependencies --timeout=240s
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=condition=WebReady --timeout=180s
# Cron fixture uses a static, single-node RWX test PV; this does NOT certify a distributed CSI backend.
docker exec "$cluster-control-plane" mkdir -p /var/local/bitrix-cron-uploads
docker exec "$cluster-control-plane" chown 979:979 /var/local/bitrix-cron-uploads
kubectl apply -f test/e2e/web/cron-storage.yaml
kubectl create --dry-run=client -f config/samples/bitrix_v1alpha1_bitrixcluster.yaml -o json |
    python3 test/e2e/web/cluster.py MySQL cron | kubectl apply -f -
kubectl -n bitrix-web-test wait bitrixcluster/cron --for=condition=CronReady --timeout=180s
kubectl -n bitrix-web-test wait bitrixcluster/cron --for=condition=WebReady --timeout=180s
kubectl -n bitrix-web-test exec deployment/cron-web -c php -- test -s /opt/www/upload/ends
test "$(kubectl -n bitrix-web-test exec deployment/cron-web -c nginx -- wget -qO- --header='Host: portal.example.com' http://cron-web/agent-mode.php)" = disabled
old_cron=$(kubectl -n bitrix-web-test get deployment/cron-cron -o jsonpath='{.spec.template.metadata.annotations.bitrix\.m8\.team/config-hash}')
kubectl -n bitrix-web-test patch bitrixcluster/cron --type=merge -p '{"spec":{"runtime":{"cron":{"agents":{"enabled":false}}}}}'
for attempt in $(seq 1 60); do
    new_cron=$(kubectl -n bitrix-web-test get deployment/cron-cron -o jsonpath='{.spec.template.metadata.annotations.bitrix\.m8\.team/config-hash}')
    [ "$new_cron" != "$old_cron" ] && break
    sleep 2
done
test "$new_cron" != "$old_cron"
kubectl -n bitrix-web-test rollout status deployment/cron-cron --timeout=120s
before=$(kubectl -n bitrix-web-test exec deployment/cron-web -c php -- cat /opt/www/upload/starts)
sleep 3
after=$(kubectl -n bitrix-web-test exec deployment/cron-web -c php -- cat /opt/www/upload/starts)
test "$before" = "$after"
kubectl -n bitrix-web-test exec deployment/cron-web -c php -- sh -ec 'test ! -e /opt/www/upload/overlap'
kubectl -n bitrix-web-test patch bitrixcluster/cron --type=merge -p '{"spec":{"runtime":{"cron":{"enabled":false}}}}'
kubectl -n bitrix-web-test wait deployment/cron-cron --for=delete --timeout=120s
kubectl -n bitrix-web-test wait bitrixcluster/cron --for=jsonpath='{.status.conditions[?(@.type=="CronReady")].reason}'=Disabled --timeout=120s
source test/e2e/web/push.sh
# Data survives CR deletion; a new CR with the same name cannot adopt it silently.
echo "Checking retained PVC and refusal to adopt it"
pvc_uid=$(kubectl -n bitrix-web-test get pvc/mysql-uploads -o jsonpath='{.metadata.uid}')
kubectl -n bitrix-web-test delete bitrixcluster/mysql
kubectl -n bitrix-web-test wait deployment/mysql-web --for=delete --timeout=120s
test "$(kubectl -n bitrix-web-test get pvc/mysql-uploads -o jsonpath='{.metadata.uid}')" = "$pvc_uid"
kubectl create --dry-run=client -f config/samples/bitrix_v1alpha1_bitrixcluster.yaml -o json |
    python3 test/e2e/web/cluster.py MySQL | kubectl apply -f -
kubectl -n bitrix-web-test wait bitrixcluster/mysql \
    --for=jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'=StorageCollision --timeout=120s
echo 'Web E2E passed: MySQL, PostgreSQL, Redis, HTTP, permissions, rotation, outage and retained uploads'
