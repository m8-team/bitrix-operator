<?php
function check(bool $ok): void { if (!$ok) { throw new RuntimeException('Runtime semantic check failed'); } }
foreach (['mysqli', 'pgsql', 'redis', 'mbstring', 'xml', 'gd', 'zip', 'intl'] as $extension) {
    check(extension_loaded($extension));
}
$m = json_decode(file_get_contents('/run/bitrix-config/manifest.json'), true, 64, JSON_THROW_ON_ERROR);
$s = require '/opt/www/bitrix/.settings.php';
$secret = file_get_contents($m['crypto']);
check($s['custom']['value']['marker'] === 'base-preserved');
check($s['custom']['value']['directory'] === '/opt/www/bitrix');
check($s['extra']['value']['marker'] === 'extra-preserved');
check($s['connections']['value']['analytics']['className'] === 'CustomConnection');
check($s['connections']['value']['default']['password'] === $secret);
check(!isset($s['connections']['value']['default']['passwordFile']));
check($s['cache']['value']['redis']['password'] === $secret);
check($s['cache']['value']['use_lock'] === true);
check($s['session']['value']['handlers']['general']['auth'] === $secret);
check($s['session']['value']['lifetime'] === 1440);
check($s['session']['value']['regenerateIdAfterLogin'] === true);
check(($s['session']['value']['handlers']['kernel'] ?? null) === ($m['sessionMode'] === 'separated' ? 'encrypted_cookies' : null));
check($s['crypto']['value']['crypto_key'] === $secret);
require '/run/bitrix-config/license_key.php';
check($LICENSE_KEY === $secret);
check((require '/opt/www/bitrix/.settings_extra.php') === []);
$pull = $s['pull']['value'];
check($s['pull']['readonly'] === true);
check($pull['guest'] === 'N');
if ($m['push']['enabled']) {
    check($pull['signature_key'] === $secret);
    check($pull['signature_algo'] === 'sha256');
    check($pull['path_to_publish'] === 'http://publisher:9010/bitrix/pub/');
    check($pull['path_to_websocket_secure'] === 'wss://#DOMAIN#/bitrix/subws/');
    check($pull['enable_protobuf'] === 'Y');
} else {
    check($pull['nginx'] === 'N' && $pull['websocket'] === 'N' && $pull['push'] === 'N');
}
echo "runtime semantics OK\n";
