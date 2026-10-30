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

# A background sampler spans both cutovers, not just the end states.
# It is *test only*: its fixed local hostname is never derived from a CR.
# Sampling can detect transient failures but cannot certify that no request
# failed between samples.
handoff_start_sampler() {
    handoff_sampler_failure="$tmp_gateway/handoff-traffic.failed"
    handoff_sampler_samples="$tmp_gateway/handoff-traffic.samples"
    rm -f "$handoff_sampler_failure" "$handoff_sampler_samples"
    (
        local actual
        while :; do
            if ! actual=$(handoff_probe_site 2>/dev/null); then
                echo "HTTPS request failed during route handoff" > "$handoff_sampler_failure"
                exit 1
            fi
            case "$actual" in
                s1|s2) ;;
                *)
                    echo "Unexpected SITE_ID during handoff: $actual" > "$handoff_sampler_failure"
                    exit 1
                    ;;
            esac
            printf '.' >> "$handoff_sampler_samples"
            sleep 0.25
        done
    ) &
    handoff_sampler_pid=$!
}
handoff_assert_sampler() {
    if [ -f "$handoff_sampler_failure" ] || ! kill -0 "$handoff_sampler_pid" 2>/dev/null; then
        if [ -f "$handoff_sampler_failure" ]; then
            cat "$handoff_sampler_failure" >&2
        fi
        echo "Continuous HTTPS handoff sampler exited unexpectedly" >&2
        return 1
    fi
}
handoff_stop_sampler() {
    handoff_assert_sampler
    kill "$handoff_sampler_pid"
    wait "$handoff_sampler_pid" 2>/dev/null || true
    handoff_sampler_pid=''
    test "$(wc -c < "$handoff_sampler_samples")" -ge 5
    test ! -f "$handoff_sampler_failure"
}
initial_route=$(handoff_route_name)
handoff_assert_only_route "$initial_route"
handoff_expect_site s1
handoff_start_sampler
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
handoff_assert_sampler
# A second change exercises the reverse handoff and verifies that URL identity
# returns to s1 only after its replacement route becomes the sole owned route.
kubectl -n bitrix-web-test patch bitrixcluster/mysql --type=json \
    -p '[{"op":"replace","path":"/spec/sites/0/id","value":"s1"}]'
wait_publication
kubectl -n bitrix-web-test wait "httproute/$new_route" --for=delete --timeout=120s
handoff_assert_only_route "$initial_route"
test "$(kubectl -n bitrix-web-test get bitrixcluster/mysql -o jsonpath='{.status.sites[0].id}')" = s1
handoff_expect_site s1
handoff_stop_sampler
echo "HTTPS handoff sampler passed: $(wc -c < "$handoff_sampler_samples") requests without observed failures"
