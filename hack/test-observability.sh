#!/usr/bin/env bash
set -Eeuo pipefail
cd "$(dirname "$0")/.."
tmp_obs=$(mktemp -d)
trap 'rm -rf "$tmp_obs"' EXIT
chmod 0755 "$tmp_obs"
go run ./hack/render-prometheus-rules config/observability/alerts.yaml > "$tmp_obs/rules.yaml"
cp test/observability/rules.test.yaml "$tmp_obs/tests.yaml"
promtool=${PROMTOOL:-}
if [ -z "$promtool" ]; then
    if [ "$(uname -s)-$(uname -m)" != Linux-x86_64 ]; then
        echo 'Set PROMTOOL to a Prometheus 3.15.0 promtool binary for your platform' >&2
        exit 1
    fi
    curl -fsSL --retry 3 --connect-timeout 10 --max-time 120 \
        https://github.com/prometheus/prometheus/releases/download/v3.15.0/prometheus-3.15.0.linux-amd64.tar.gz \
        -o "$tmp_obs/prometheus.tar.gz"
    echo "2a542df32eac02ee17b9d844fb2aa1de00dafa5476579ba8a3ba862e9d572ea0  $tmp_obs/prometheus.tar.gz" | sha256sum -c -
    tar --no-same-owner -xzf "$tmp_obs/prometheus.tar.gz" -C "$tmp_obs" --strip-components=1 prometheus-3.15.0.linux-amd64/promtool
    promtool="$tmp_obs/promtool"
fi
"$promtool" test rules "$tmp_obs/tests.yaml"
