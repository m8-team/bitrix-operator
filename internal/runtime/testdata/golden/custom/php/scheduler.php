<?php
// Duration-based fixed delay: one fresh PHP process per invocation, no catch-up backlog.
// SIGTERM stops scheduling and waits for the active child within the Pod's grace period.
try {
    if (!function_exists('proc_open')) {
        throw new RuntimeException('required scheduler functions unavailable');
    }
    $config = json_decode(file_get_contents(__DIR__ . '/manifest.json'), true, 512, JSON_THROW_ON_ERROR)['cron'];
    $interval = (float)$config['intervalSeconds'];
    if (!$config['enabled'] || $interval < 1 || $interval > 86400) {
        throw new RuntimeException('invalid scheduler configuration');
    }
    $stopFile = $argv[1];
    @unlink('/tmp/cron-ready');
    $heartbeat = static function (): void {
        if (!touch('/tmp/cron-heartbeat')) {
            throw new RuntimeException('heartbeat unavailable');
        }
    };
    $next = 0;
    while (!is_file($stopFile)) {
        $heartbeat();
        if (!$config['agents']) {
            touch('/tmp/cron-ready');
        } elseif (hrtime(true) >= $next) {
            // Array command bypasses a shell. Application output may contain credentials: suppress it.
            $child = proc_open([PHP_BINARY, '-d', 'display_errors=0', '-d', 'log_errors=0', __DIR__ . '/agents.php'],
                [0 => ['file', '/dev/null', 'r'], 1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']], $pipes);
            if (!is_resource($child)) {
                throw new RuntimeException('worker unavailable');
            }
            do {
                $heartbeat();
                $status = proc_get_status($child);
                if ($status['running']) {
                    usleep(100000);
                }
            } while ($status['running']);
            $exitCode = $status['exitcode'];
            proc_close($child);
            if ($exitCode === 0) {
                touch('/tmp/cron-ready');
            } else {
                @unlink('/tmp/cron-ready');
                fwrite(STDERR, $exitCode === 75 ? "Agents lock is busy\n" : "Agents execution failed\n");
            }
            $next = hrtime(true) + (int)($interval * 1000000000);
        }
        usleep(100000);
        clearstatcache();
    }
    @unlink('/tmp/cron-ready');
} catch (Throwable $error) {
    @unlink('/tmp/cron-ready');
    fwrite(STDERR, "Agents scheduler unavailable\n");
    exit(1);
}
