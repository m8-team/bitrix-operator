#!/bin/sh
set -eu
resolver=$(awk '$1 == "nameserver" {print $2; exit}' /etc/resolv.conf)
case "$resolver" in ''|*[!0-9a-fA-F.:]*) echo 'Invalid DNS resolver' >&2; exit 1;; esac
case "$resolver" in *:*) resolver="[$resolver]";; esac
sed "s/@RESOLVER@/$resolver/" /run/nginx/resolver.conf > /tmp/push-resolver.conf
search=$(awk '$1 == "search" {print $2; exit}' /etc/resolv.conf)
# Upstreams are namespace-local Services. Expand using the Pod's first DNS search suffix.
if grep -q '@SEARCH@' /run/nginx/nginx.conf; then
 case "$search" in ''|*[!a-zA-Z0-9.-]*) echo 'Invalid DNS search suffix' >&2; exit 1;; esac
fi
sed "s/@SEARCH@/$search/g" /run/nginx/nginx.conf > /tmp/nginx.conf
exec nginx -g 'daemon off;' -c /tmp/nginx.conf
