#!/bin/sh
set -eu
node /run/push-config/configure.cjs "$1"
cd /opt/push-server
exec node server.js --config /tmp/push/config.json
