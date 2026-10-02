# Sourced only by hack/e2e-web.sh in its newly created, isolated kind cluster.
# Subshell owns local temporary keys and port-forward; outer trap owns the cluster.
(
set -Eeuo pipefail
tmp_gateway=$(mktemp -d)
forward_pid=''
cleanup_gateway() {
    if [ -n "$forward_pid" ]; then kill "$forward_pid" 2>/dev/null || true; fi
    rm -rf "$tmp_gateway"
}
trap cleanup_gateway EXIT
wait_publication() {
    kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=jsonpath='{.status.observedGeneration}'="$(kubectl -n bitrix-web-test get bitrixcluster/mysql -o jsonpath='{.metadata.generation}')" --timeout=120s
    kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=condition=Ready --timeout=300s
}
assert_no_routes() {
    test "$(kubectl -n bitrix-web-test get httproutes -o name | wc -l)" -eq 0
}
certificate() {
    openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 2 \
        -subj /CN=portal.example.com -addext subjectAltName=DNS:portal.example.com \
        -keyout "$tmp_gateway/key.pem" -out "$tmp_gateway/cert.pem" 2>/dev/null
    kubectl -n bitrix-web-test create secret tls site-tls --cert="$tmp_gateway/cert.pem" --key="$tmp_gateway/key.pem" --dry-run=client -o yaml | kubectl apply -f -
}
echo 'Checking optional Gateway APIs and late installation'
kubectl -n bitrix-web-test patch bitrixcluster/mysql --type=merge -p '{"spec":{"networking":{"gateway":{"enabled":true,"parentRef":{"name":"bitrix-e2e","namespace":"envoy-gateway-system","sectionName":"web"}}}}}'
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=jsonpath='{.status.conditions[?(@.type=="GatewayReady")].reason}'=DependencyMissing --timeout=120s
curl --fail --silent --show-error --location --retry 3 -o "$tmp_gateway/install.yaml" https://github.com/envoyproxy/gateway/releases/download/v1.9.2/install.yaml
echo "0412a72907e57ff9b73c56a7bf6df5190bf0f6e4f8bb4bba34e38630bbab5778  $tmp_gateway/install.yaml" | sha256sum -c -
kubectl apply --server-side -f "$tmp_gateway/install.yaml"
kubectl wait crd/gateways.gateway.networking.k8s.io crd/httproutes.gateway.networking.k8s.io crd/referencegrants.gateway.networking.k8s.io --for=condition=Established --timeout=120s
kubectl -n envoy-gateway-system rollout status deployment/envoy-gateway --timeout=240s
kubectl -n bitrix-operator-system rollout restart deployment/bitrix-operator-controller-manager
kubectl -n bitrix-operator-system rollout status deployment/bitrix-operator-controller-manager --timeout=180s
kubectl apply -f test/e2e/web/gateway.yaml
wait_publication
test "$(kubectl -n bitrix-web-test get bitrixcluster/mysql -o jsonpath='{.status.sites[0].urls[0]}')" = http://portal.example.com
service=$(kubectl -n envoy-gateway-system get service -l gateway.envoyproxy.io/owning-gateway-name=bitrix-e2e,gateway.envoyproxy.io/owning-gateway-namespace=envoy-gateway-system -o jsonpath='{.items[0].metadata.name}')
response=$(kubectl -n bitrix-web-test exec deployment/mysql-web -c nginx -- wget -qO- --header='Host: portal.example.com' --header='X-Forwarded-Proto: https' "http://$service.envoy-gateway-system/transport.php")
python3 -c 'import json,sys; assert json.loads(sys.argv[1]) == {"scheme":"http","https":"","port":"80"}' "$response"
echo 'Checking HTTPS and cross-namespace certificate permissions'
certificate
kubectl -n envoy-gateway-system patch gateway/bitrix-e2e --type=json -p '[{"op":"replace","path":"/spec/listeners/0/protocol","value":"HTTPS"},{"op":"replace","path":"/spec/listeners/0/port","value":443},{"op":"add","path":"/spec/listeners/0/tls","value":{"mode":"Terminate","certificateRefs":[{"name":"site-tls","namespace":"bitrix-web-test"}]}}]'
kubectl -n bitrix-web-test patch bitrixcluster/mysql --type=json -p '[{"op":"add","path":"/spec/sites/0/tls","value":{"secretName":"site-tls"}}]'
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=jsonpath='{.status.conditions[?(@.type=="GatewayReady")].reason}'=ReferenceNotPermitted --timeout=120s
assert_no_routes
kubectl apply -f test/e2e/web/gateway-grant.yaml
wait_publication
test "$(kubectl -n bitrix-web-test get bitrixcluster/mysql -o jsonpath='{.status.sites[0].urls[0]}')" = https://portal.example.com
kubectl -n envoy-gateway-system port-forward "service/$service" 9443:443 >"$tmp_gateway/forward.log" 2>&1 &
forward_pid=$!
https_probe() {
    curl --fail --silent --show-error --noproxy '*' --connect-timeout 2 --max-time 5 \
        --resolve portal.example.com:9443:127.0.0.1 --cacert "$tmp_gateway/cert.pem" \
        -H 'X-Forwarded-Proto: http' -H 'X-Forwarded-Port: 80' https://portal.example.com:9443/transport.php
}
for attempt in $(seq 1 60); do
    if response=$(https_probe 2>/dev/null); then break; fi
    sleep 2
done
python3 -c 'import json,sys; assert json.loads(sys.argv[1]) == {"scheme":"https","https":"on","port":"443"}' "$response"
echo 'Checking certificate rotation without an application rollout'
revision=$(kubectl -n bitrix-web-test get deployment/mysql-web -o jsonpath='{.spec.template.metadata.annotations.bitrix\.m8\.team/secret-revision}')
generation=$(kubectl -n bitrix-web-test get deployment/mysql-web -o jsonpath='{.metadata.generation}')
certificate
# Each certificate uses a fresh key: trusting only the new cert proves Envoy rotation.
for attempt in $(seq 1 60); do
    if https_probe > /dev/null 2>&1; then break; fi
    sleep 2
done
https_probe > /dev/null
test "$(kubectl -n bitrix-web-test get deployment/mysql-web -o jsonpath='{.spec.template.metadata.annotations.bitrix\.m8\.team/secret-revision}')" = "$revision"
test "$(kubectl -n bitrix-web-test get deployment/mysql-web -o jsonpath='{.metadata.generation}')" = "$generation"
echo 'Checking revoked permission withdraws publication'
kubectl -n bitrix-web-test delete referencegrant/site-certificate
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=jsonpath='{.status.conditions[?(@.type=="GatewayReady")].reason}'=ReferenceNotPermitted --timeout=120s
assert_no_routes
test -z "$(kubectl -n bitrix-web-test get bitrixcluster/mysql -o jsonpath='{.status.sites[0].urls[0]}')"
kubectl apply -f test/e2e/web/gateway-grant.yaml
wait_publication
gateway_spec=$(kubectl -n envoy-gateway-system get gateway/bitrix-e2e -o jsonpath='{.spec}')
kubectl -n bitrix-web-test patch bitrixcluster/mysql --type=merge -p '{"spec":{"networking":{"gateway":{"enabled":false}}}}'
kubectl -n bitrix-web-test wait bitrixcluster/mysql --for=jsonpath='{.status.conditions[?(@.type=="GatewayReady")].reason}'=Disabled --timeout=120s
assert_no_routes
test "$(kubectl -n envoy-gateway-system get gateway/bitrix-e2e -o jsonpath='{.spec}')" = "$gateway_spec"
echo 'Gateway E2E passed: absent CRDs, HTTP, HTTPS, cross-namespace permissions, TLS rotation and withdrawal'
)
