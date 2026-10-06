<?php
// Synthetic E2E endpoint only; never shipped in a production Bitrix release.
file_put_contents('/opt/www/upload/drain-started', 'started');
$deadline = microtime(true) + 90;
while (!file_exists('/opt/www/upload/drain-release')) {
    if (microtime(true) > $deadline) {
        http_response_code(500);
        exit('drain test timed out');
    }
    usleep(100000);
    clearstatcache();
}
file_put_contents('/opt/www/upload/drain-finished', 'finished');
echo 'request drained';
