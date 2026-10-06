#!/bin/sh
set -eu
# Kubernetes starts regular-container hooks in arbitrary order. Wait for nginx
# workers (not merely its listening socket) before gracefully stopping FPM.
# Bound the wait for a crashed nginx master that left a stale pidfile.
remaining=320
while [ -f /run/nginx-state/nginx.pid ] && [ "$remaining" -gt 0 ]; do
    sleep 1
    remaining=$((remaining - 1))
done
kill -QUIT 1
remaining=25
while kill -0 1 2>/dev/null && [ "$remaining" -gt 0 ]; do
    sleep 1
    remaining=$((remaining - 1))
done
