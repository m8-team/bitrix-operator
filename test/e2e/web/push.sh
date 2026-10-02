#!/usr/bin/env bash
# Sourced only by hack/e2e-web.sh inside its own disposable kind cluster.
# kindnet does not enforce NetworkPolicy: this covers API resources and protocol,
# not CNI enforcement. The Node probe runs inside publisher to reuse upstream dependencies.
kubectl -n bitrix-web-test apply -f config/examples/push-default-deny.yaml
push_image=quay.io/bitrix24/push:3.3-v1-alpine@sha256:7ea0b71d66ab6cc9b0b58b6ba853d4398880fdfb1aa19b434a3bf462043bfecc
kubectl -n bitrix-web-test create secret generic bitrix-push --from-literal=key=synthetic-signing-key-0123456789abcdef0123456789abcdef
kubectl -n bitrix-web-test patch bitrixcluster/postgresql --type=merge -p \
    "{\"spec\":{\"runtime\":{\"push\":{\"enabled\":true,\"image\":\"$push_image\",\"secretRef\":{\"name\":\"bitrix-push\",\"key\":\"key\"},\"publisher\":{\"replicas\":1},\"subscriber\":{\"replicas\":2}}}}}"
wait_push() {
    for attempt in $(seq 1 120); do
        if kubectl -n bitrix-web-test get bitrixcluster/postgresql -o json | python3 -c '
import json,sys
c=json.load(sys.stdin)
conditions={v["type"]:v for v in c["status"]["conditions"]}
assert all(conditions[t]["status"]=="True" and conditions[t]["observedGeneration"]==c["metadata"]["generation"] for t in ("PushReady","WebReady"))
' 2>/dev/null; then return; fi
        sleep 2
    done
    echo 'Current Push/web generation did not converge' >&2
    return 1
}
check_push() {
    # Real WebSocket/long polling and signed REST traverse the public nginx Service.
    kubectl -n bitrix-web-test exec -i deployment/postgresql-push-publisher -c publisher -- \
        node - http://postgresql-push-publisher:9010 http://postgresql-web,http://postgresql-web http://postgresql-web /bitrix/sub/ \
        < internal/runtime/testdata/check-push.cjs
    kubectl -n bitrix-web-test exec deployment/postgresql-web -c nginx -- sh -ec '
        for uri in /bitrix/pub/ /bitrix/PUB/ /server-stat/; do
            if wget -S -O /tmp/body --header="Host: portal.example.com" "http://postgresql-web$uri" 2>/tmp/headers; then exit 1; fi
            grep -q "403 Forbidden" /tmp/headers
        done
    '
    kubectl -n bitrix-web-test exec deployment/postgresql-web -c php -- php -r '
        $s = require "/opt/www/bitrix/.settings.php";
        $p = $s["pull"]["value"];
        if ($p["nginx"] !== "Y" || $p["signature_algo"] !== "sha256" || $p["path_to_publish"] !== "http://postgresql-push-publisher:9010/bitrix/pub/") {exit(1);}
    '
}
wait_push
check_push
# Change both replica counts and verify the desired rollout before exercising reconnect again.
kubectl -n bitrix-web-test patch bitrixcluster/postgresql --type=merge -p '{"spec":{"runtime":{"push":{"publisher":{"replicas":2},"subscriber":{"replicas":3}}}}}'
wait_push
check_push
# Secret watches must roll all three Deployments (conditions alone could still reflect old Secret revisions).
old_push=$(kubectl -n bitrix-web-test get deployment/postgresql-web -o jsonpath='{.spec.template.metadata.annotations.bitrix\.m8\.team/secret-revision}')
kubectl -n bitrix-web-test patch secret bitrix-push --type=merge -p '{"stringData":{"key":"rotated-signing-key-0123456789abcdef0123456789abcdef"}}'
for attempt in $(seq 1 60); do
    new_push=$(kubectl -n bitrix-web-test get deployment/postgresql-web -o jsonpath='{.spec.template.metadata.annotations.bitrix\.m8\.team/secret-revision}')
    [ "$old_push" != "$new_push" ] && break
    sleep 2
done
test "$old_push" != "$new_push"
for name in postgresql-web postgresql-push-publisher postgresql-push-subscriber; do
    test "$(kubectl -n bitrix-web-test get deployment/$name -o jsonpath='{.spec.template.metadata.annotations.bitrix\.m8\.team/secret-revision}')" = "$new_push"
    kubectl -n bitrix-web-test rollout status deployment/$name --timeout=180s
done
wait_push
check_push
kubectl -n bitrix-web-test patch bitrixcluster/postgresql --type=merge -p '{"spec":{"runtime":{"push":{"enabled":false}}}}'
for role in publisher subscriber; do
    kubectl -n bitrix-web-test wait deployment/postgresql-push-$role --for=delete --timeout=120s
done
kubectl -n bitrix-web-test wait bitrixcluster/postgresql --for=jsonpath='{.status.conditions[?(@.type=="PushReady")].reason}'=Disabled --timeout=120s
wait_push
for role in publisher subscriber; do
    test -z "$(kubectl -n bitrix-web-test get service/postgresql-push-$role networkpolicy/postgresql-push-$role --ignore-not-found -o name)"
done
kubectl -n bitrix-web-test exec deployment/postgresql-web -c php -- php -r '
    $s = require "/opt/www/bitrix/.settings.php";
    if ($s["pull"]["value"]["nginx"] !== "N") {exit(1);}
'
echo 'Push E2E passed: nginx WS/polling/REST, HMAC, scale, rotation and disable'
