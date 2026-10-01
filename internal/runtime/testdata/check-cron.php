<?php
// Runs inside the pinned native PHP image as UID 979 with a read-only root.
function check(bool $ok, string $message): void {
    if (!$ok) { throw new RuntimeException($message); }
}
function until(callable $predicate): void {
    $deadline = microtime(true) + 15;
    do {
        clearstatcache();
        if ($predicate()) { return; }
        usleep(100000);
    } while (microtime(true) < $deadline);
    throw new RuntimeException('timed out');
}
function countLines(string $name): int {
    return is_file('/opt/www/upload/' . $name) ? count(file('/opt/www/upload/' . $name)) : 0;
}
function startScheduler() {
    $child = proc_open([PHP_BINARY, '/run/bitrix-config/scheduler.php'],
        [0 => ['file', '/dev/null', 'r'], 1 => ['file', '/dev/null', 'w'], 2 => ['file', '/tmp/cron-log', 'a']], $pipes);
    check(is_resource($child), 'spawn failed');
    return $child;
}
function stopScheduler($child): void {
    proc_terminate($child, SIGTERM);
    until(static fn() => !proc_get_status($child)['running']);
    proc_close($child);
}
$config = json_decode(file_get_contents('/run/bitrix-config/manifest.json'), true)['cron'];
$children = [];
try {
    $children[] = $first = startScheduler();
    if (!$config['agents']) {
        until(static fn() => is_file('/tmp/cron-ready'));
        usleep(2200000);
        check(countLines('starts') === 0, 'disabled agents executed');
        stopScheduler($first);
        $children = [];
        echo "cron disabled OK\n";
        exit(0);
    }
    until(static fn() => countLines('starts') === 1);
    $children[] = $second = startScheduler();
    until(static fn() => is_file('/tmp/cron-log') && str_contains(file_get_contents('/tmp/cron-log'), 'lock is busy'));
    stopScheduler($second);
    array_pop($children);
    until(static fn() => countLines('ends') >= 2);
    // The delay starts after completion, so a long agent never causes backlogged catch-up.
    $starts = array_map('intval', file('/opt/www/upload/starts'));
    $ends = array_map('intval', file('/opt/www/upload/ends'));
    check($starts[1] - $ends[0] >= 1200000000, 'interval violated');
    check(!is_file('/opt/www/upload/overlap'), 'concurrent invocation');
    touch('/opt/www/upload/fail');
    until(static fn() => str_contains(file_get_contents('/tmp/cron-log'), 'execution failed'));
    check(!is_file('/tmp/cron-ready'), 'failed worker remained ready');
    check(!str_contains(file_get_contents('/tmp/cron-log'), 'synthetic-secret'), 'worker error leaked');
    unlink('/opt/www/upload/fail');
    until(static fn() => is_dir('/opt/www/upload/active'));
    stopScheduler($first); // SIGTERM while an agent is running must wait for completion.
    $children = [];
    check(!is_dir('/opt/www/upload/active'), 'graceful stop abandoned the worker');
    check(countLines('starts') === countLines('ends'), 'unfinished invocation');
    $children[] = $restarted = startScheduler();
    $old = countLines('ends');
    until(static fn() => countLines('ends') > $old);
    stopScheduler($restarted);
    $children = [];
    check(!is_file('/opt/www/upload/overlap'), 'restart overlap');
    echo "cron scheduling and locking OK\n";
} finally {
    foreach ($children as $child) {
        if (is_resource($child)) { proc_terminate($child, SIGKILL); proc_close($child); }
    }
}
