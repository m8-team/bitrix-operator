<?php
// A long-running agent must not trigger liveness restarts. The supervisor updates this heartbeat.
$heartbeat = @filemtime('/tmp/cron-heartbeat');
$live = $heartbeat !== false && time() - $heartbeat < 20;
exit($live && (($argv[1] ?? '') === 'live' || is_file('/tmp/cron-ready')) ? 0 : 1);
