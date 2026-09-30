<?php
// Readiness checks configuration and external transports, not Bitrix site existence.
declare(strict_types=1);
ini_set('display_errors', '0');
ini_set('log_errors', '0');
set_error_handler(static function (): never { throw new RuntimeException('Dependency unavailable'); });
try {
    $m = json_decode(file_get_contents(__DIR__ . '/manifest.json'), true, 64, JSON_THROW_ON_ERROR);
    $settings = require '/opt/www/bitrix/.settings.php';
    require __DIR__ . '/license_key.php';
    foreach ($m['connections'] as $name => $_) {
        $c = $settings['connections']['value'][$name];
        [$host, $port] = explode(':', $c['host'], 2);
        if ($m['engine'] === 'MySQL') {
            mysqli_report(MYSQLI_REPORT_ERROR | MYSQLI_REPORT_STRICT);
            $db = mysqli_init();
            $db->options(MYSQLI_OPT_CONNECT_TIMEOUT, 2);
            $db->options(MYSQLI_OPT_READ_TIMEOUT, 2);
            $db->real_connect($host, $c['login'], $c['password'], $c['database'], (int) $port);
            if ($c['initCommand'] !== '') { $db->query($c['initCommand']); }
            $db->query('SELECT 1');
            $db->close();
        } else {
            // libpq keyword values use backslash escaping inside single quotes.
            $quote = static fn(string $v): string => "'" . str_replace(['\\', "'"], ['\\\\', "\\'"], $v) . "'";
            $dsn = 'host=' . $quote($host) . ' port=' . (int) $port . ' dbname=' . $quote($c['database'])
                . ' user=' . $quote($c['login']) . ' password=' . $quote($c['password']) . ' connect_timeout=2 options=' . $quote('-c statement_timeout=2000');
            $db = pg_connect($dsn, PGSQL_CONNECT_FORCE_NEW);
            if ($db === false) { throw new RuntimeException('Dependency unavailable'); }
            if ($c['initCommand'] !== '' && pg_query($db, $c['initCommand']) === false) { throw new RuntimeException('Dependency unavailable'); }
            if (pg_query($db, 'SELECT 1') === false) { throw new RuntimeException('Dependency unavailable'); }
            pg_close($db);
        }
    }
    $c = $settings['cache']['value']['redis'];
    $redis = new Redis();
    if (!$redis->connect($c['host'], (int) $c['port'], 2, null, 0, 2) || !$redis->auth($c['password']) || !$redis->ping()) {
        throw new RuntimeException('Dependency unavailable');
    }
    $redis->close();
    http_response_code(200);
    echo "ready\n";
} catch (Throwable $e) {
    http_response_code(503);
    echo "unavailable\n";
    exit(1);
}
