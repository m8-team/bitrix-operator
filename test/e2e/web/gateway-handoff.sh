# Sourced by gateway.sh after successful TLS installation, port-forward and
# certificate rotation. This checks actual Envoy HTTPS routing and PHP site ID
# after replacement routes are the only owned routes remaining.
echo 'Checking HTTPS data-plane routing across HTTPRoute handoff'
handoff_route_name() {
    kubectl -n bitrix-web-test get httproutes -o jsonpath='{.items[0].metadata.name}'
}
handoff_probe_site() {
    # Fixed fixture hostname and target. Never source a URL/host from a CR
    # into an unrestricted manager HTTP client.
    curl --fail --silent --show-error --noproxy '*' --connect-timeout 2 --max-time 5 \
        --resolve portal.example.com:9443:127.0.0.1 --cacert "$tmp_gateway/cert.pem" \
        https://portal.example.com:9443/site.php
}
handoff_expect_site() {
    local expected=$1 actual='' attempt
    for attempt in $(seq 1 60); do
        if actual=$(handoff_probe_site 2>/dev/null) && [ "$actual" = "$expected" ]; then
            return 0
        fi
        sleep 2
    done
    echo "Expected site $expected through Envoy HTTPS, got '$actual'" >&2
    return 1
}
handoff_assert_only_route() {
    local expected=$1
    test "$(kubectl -n bitrix-web-test get httproutes -o name | wc -l)" -eq 1
    test "$(handoff_route_name)" = "$expected"
}
initial_route=$(handoff_route_name)
handoff_assert_only_route "$initial_route"
handoff_expect_site s1
# The fixture contains site IDs s1 and s2. Changing a site ID changes the
# deterministic HTTPRoute name while retaining the same public hostname.
# Old/new routes may coexist: a successful probe alone can reach the OLD route
# because Gateway API prioritizes older equal-precedence route matches.
kubectl -n bitrix-web-test patch bitrixcluster/mysql --type=json \
    -p '[{"op":"replace","path":"/spec/sites/0/id","value":"s2"}]'
wait_publication
kubectl -n bitrix-web-test wait "httproute/$initial_route" --for=delete --timeout=120s
new_route=$(handoff_route_name)
test "$new_route" != "$initial_route"
handoff_assert_only_route "$new_route"
test "$(kubectl -n bitrix-web-test get bitrixcluster/mysql -o jsonpath='{.status.sites[0].id}')" = s2
handoff_expect_site s2
# A second change exercises the reverse handoff and verifies that URL identity
# returns to s1 only after its replacement route becomes the sole owned route.
kubectl -n bitrix-web-test patch bitrixcluster/mysql --type=json \
    -p '[{"op":"replace","path":"/spec/sites/0/id","value":"s1"}]'
wait_publication
kubectl -n bitrix-web-test wait "httproute/$new_route" --for=delete --timeout=120s
handoff_assert_only_route "$initial_route"
test "$(kubectl -n bitrix-web-test get bitrixcluster/mysql -o jsonpath='{.status.sites[0].id}')" = s1
handoff_expect_site s1
