# Runs only in the new isolated kind cluster created by e2e-web.sh.
(
set -Eeuo pipefail
tmp_metrics=$(mktemp -d)
forward_pid=''
cleanup_metrics() {
    if [ -n "$forward_pid" ]; then kill "$forward_pid" 2>/dev/null || true; wait "$forward_pid" 2>/dev/null || true; fi
    rm -rf "$tmp_metrics"
}
trap cleanup_metrics EXIT
namespace=bitrix-operator-system
kubectl -n "$namespace" create serviceaccount metrics-test
kubectl -n "$namespace" create serviceaccount metrics-denied
kubectl create clusterrolebinding bitrix-metrics-test --clusterrole=bitrix-operator-metrics-reader --serviceaccount="$namespace:metrics-test"
for account in metrics-test metrics-denied; do
    token=$(kubectl -n "$namespace" create token "$account" --duration=10m)
    printf 'header = "Authorization: Bearer %s"\n' "$token" > "$tmp_metrics/$account.conf"
    chmod 0600 "$tmp_metrics/$account.conf"
done
unset token
kubectl -n "$namespace" port-forward service/bitrix-operator-controller-manager-metrics-service 18443:8443 > "$tmp_metrics/forward.log" 2>&1 &
forward_pid=$!
# -k is confined to the disposable self-signed fixture, never the production profile.
for attempt in $(seq 1 30); do
    code=$(curl -sk --max-time 3 -o /dev/null -w '%{http_code}' https://127.0.0.1:18443/metrics || true)
    [ "$code" = 401 ] && break
    sleep 1
done
test "$code" = 401
test "$(curl -sk --max-time 10 --config "$tmp_metrics/metrics-denied.conf" -o /dev/null -w '%{http_code}' https://127.0.0.1:18443/metrics)" = 403
curl -fsk --max-time 10 --config "$tmp_metrics/metrics-test.conf" https://127.0.0.1:18443/metrics > "$tmp_metrics/metrics"
grep -Fxq 'bitrix_operator_collection_success 1' "$tmp_metrics/metrics"
grep -Fxq 'bitrix_cluster_condition{cluster="mysql",cluster_namespace="bitrix-web-test",condition="Ready",status="True"} 1' "$tmp_metrics/metrics"
if grep -q 'fixture-password\|fixture-root-only' "$tmp_metrics/metrics"; then echo 'Credential leaked into metrics' >&2; exit 1; fi
events_ready=false
for attempt in $(seq 1 30); do
    kubectl -n bitrix-web-test get events.events.k8s.io -o json > "$tmp_metrics/events.json"
    if python3 - "$tmp_metrics/events.json" <<'PY'
import json, sys
items = json.load(open(sys.argv[1]))['items']
events = [e for e in items if e.get('reportingController') == 'bitrix.m8.team/bitrixcluster-controller' and e['regarding']['name'] == 'mysql' and e['reason'] == 'ClusterReady']
assert events and all(e['type'] == 'Normal' and e['action'] == 'Reconcile' for e in events)
assert 'fixture-password' not in json.dumps(events)
PY
    then events_ready=true; break; fi
    sleep 1
done
test "$events_ready" = true
echo 'Observability E2E passed: HTTPS authn/authz, current conditions, sanitized metrics and events.k8s.io/v1 Events'
)
