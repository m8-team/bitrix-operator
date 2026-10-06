#!/bin/sh
set -eu
# Let EndpointSlice/Gateway consumers observe termination before closing listeners.
touch /tmp/draining
sleep 10
# nginx is PID 1 (start.sh uses exec). QUIT finishes accepted requests, including
# FastCGI; the master removes /tmp/nginx.pid only after its workers have exited.
kill -QUIT 1
# Keep preStop running so the runtime cannot send its default stop signal early.
remaining=310
while kill -0 1 2>/dev/null && [ "$remaining" -gt 0 ]; do
    sleep 1
    remaining=$((remaining - 1))
done
