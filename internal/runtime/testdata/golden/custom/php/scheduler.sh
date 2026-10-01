#!/bin/sh
# PID 1 handles signals without requiring the optional PHP pcntl extension.
set -eu
stop_file="/tmp/cron-stop-$$"
rm -f "$stop_file"
trap 'touch "$stop_file"' TERM INT QUIT
php /run/bitrix-config/scheduler.php "$stop_file" &
scheduler=$!
result=0
while :; do
    # A trapped signal interrupts wait; wait again for PHP and its active worker to finish.
    set +e
    wait "$scheduler"
    result=$?
    set -e
    kill -0 "$scheduler" 2>/dev/null || break
done
rm -f "$stop_file"
exit "$result"
