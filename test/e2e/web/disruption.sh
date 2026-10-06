# Sourced after HPA is disabled and cron-web has two healthy fixed replicas.
(
set -Eeuo pipefail
tmp_drain=$(mktemp -d)
drain_pid=''
request_pid=''
cleanup_drain() {
    kubectl uncordon "$cluster-control-plane" "$cluster-worker" || true
    if [ -n "$drain_pid" ]; then kill "$drain_pid" 2>/dev/null || true; fi
    if [ -n "$request_pid" ]; then kill "$request_pid" 2>/dev/null || true; fi
    rm -rf "$tmp_drain"
}
trap cleanup_drain EXIT
uid=$(kubectl -n bitrix-web-test get bitrixcluster/cron -o jsonpath='{.metadata.uid}')
selector="bitrix.m8.team/owner-uid=$uid,app.kubernetes.io/name=bitrix"
echo 'Checking web topology and live disruption budget'
kubectl -n bitrix-web-test get pods -l "$selector" -o json > "$tmp_drain/pods.json"
python3 - "$tmp_drain/pods.json" > "$tmp_drain/targets" <<'PY'
import json, sys
pods = [p for p in json.load(open(sys.argv[1]))['items'] if not p['metadata'].get('deletionTimestamp')]
assert len(pods) == 2 and len({p['spec']['nodeName'] for p in pods}) == 2, 'web replicas must spread across both available nodes'
for p in pods:
    print(p['metadata']['name'], p['spec']['nodeName'], p['status']['podIP'])
PY
read -r target target_node target_ip < "$tmp_drain/targets"
read -r survivor survivor_node _ < <(tail -n 1 "$tmp_drain/targets")
kubectl -n bitrix-web-test wait pdb/cron-web --for=jsonpath='{.status.disruptionsAllowed}'=1 --timeout=90s
kubectl -n bitrix-web-test exec "$survivor" -c php -- sh -ec 'rm -f /opt/www/upload/drain-started /opt/www/upload/drain-finished /opt/www/upload/drain-release; echo retained > /opt/www/upload/drain-data'
# Connect directly so we know which Pod owns the in-flight request.
kubectl -n bitrix-web-test exec "$survivor" -c nginx -- wget -T 110 -qO- --header='Host: portal.example.com' "http://$target_ip:8080/drain.php" > "$tmp_drain/response" &
request_pid=$!
for attempt in $(seq 1 30); do
    if kubectl -n bitrix-web-test exec "$survivor" -c php -- test -f /opt/www/upload/drain-started; then break; fi
    sleep 1
done
kubectl -n bitrix-web-test exec "$survivor" -c php -- test -f /opt/www/upload/drain-started
# Hold replacement scheduling only until the second eviction is checked.
kubectl cordon "$survivor_node"
kubectl drain "$target_node" --pod-selector "$selector" --ignore-daemonsets --delete-emptydir-data --timeout=180s > "$tmp_drain/drain.log" 2>&1 &
drain_pid=$!
kubectl -n bitrix-web-test wait pdb/cron-web --for=jsonpath='{.status.disruptionsAllowed}'=0 --timeout=30s
cat > "$tmp_drain/eviction.json" <<JSON
{"apiVersion":"policy/v1","kind":"Eviction","metadata":{"name":"$survivor","namespace":"bitrix-web-test"}}
JSON
if kubectl create --raw "/api/v1/namespaces/bitrix-web-test/pods/$survivor/eviction" -f "$tmp_drain/eviction.json" > "$tmp_drain/eviction.log" 2>&1; then
    echo 'PDB allowed simultaneous eviction of both healthy replicas' >&2; exit 1
fi
grep -q 'disruption budget' "$tmp_drain/eviction.log"
kubectl uncordon "$survivor_node"
test "$(kubectl -n bitrix-web-test exec "$survivor" -c nginx -- wget -qO- --header='Host: portal.example.com' http://cron-web/)" = runtime-fixture
# Wait for QUIT to close nginx listeners while the accepted FastCGI request is
# still held. This proves FPM survives beyond the endpoint propagation delay.
for attempt in $(seq 1 30); do
    if ! kubectl -n bitrix-web-test exec "$survivor" -c nginx -- wget -T 2 -qO- "http://$target_ip:8081/live"; then break; fi
    sleep 1
done
if kubectl -n bitrix-web-test exec "$survivor" -c nginx -- wget -T 2 -qO- "http://$target_ip:8081/live"; then
    echo 'nginx did not stop accepting connections during drain' >&2; exit 1
fi
kubectl -n bitrix-web-test exec "$target" -c php -- sh -ec 'test -f /run/nginx-state/nginx.pid; test ! -f /opt/www/upload/drain-finished'
kubectl -n bitrix-web-test exec "$survivor" -c php -- touch /opt/www/upload/drain-release
wait "$request_pid"
request_pid=''
test "$(cat "$tmp_drain/response")" = 'request drained'
wait "$drain_pid"
drain_pid=''
kubectl -n bitrix-web-test rollout status deployment/cron-web --timeout=180s
kubectl -n bitrix-web-test wait bitrixcluster/cron --for=condition=Ready --timeout=180s
kubectl -n bitrix-web-test get pods -l "$selector" -o json | python3 -c \
    'import json,sys; p=json.load(sys.stdin)["items"]; assert len(p)==2; assert all(x["spec"]["nodeName"]==sys.argv[1] for x in p)' "$survivor_node"
test "$(kubectl -n bitrix-web-test exec deployment/cron-web -c php -- cat /opt/www/upload/drain-data)" = retained
test "$(kubectl -n bitrix-web-test exec deployment/cron-web -c php -- cat /opt/www/upload/drain-finished)" = finished
echo 'Disruption E2E passed: spread, PDB rejection, active PHP request, cross-node replacement and uploads'
)
