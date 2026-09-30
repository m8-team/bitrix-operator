<?php
// Check the local FPM listener only. Dependency outages must not restart PHP.
ini_set('display_errors', '0');
ini_set('log_errors', '0');
$socket = @fsockopen('127.0.0.1', 9000, $code, $message, 1);
if ($socket === false) { exit(1); }
fclose($socket);
