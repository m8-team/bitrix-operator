<?php
// Mounted over /opt/www/bitrix/license_key.php in PHP containers only.
declare(strict_types=1);
$LICENSE_KEY = (static function (): string {
    $m = json_decode(file_get_contents('/run/bitrix-config/manifest.json'), true, 64, JSON_THROW_ON_ERROR);
    $path = $m['license'] ?? '';
    if (!preg_match('~^/run/bitrix-secrets/[a-f0-9]{64}$~D', $path)) {
        throw new RuntimeException('Invalid runtime Secret path');
    }
    $value = @file_get_contents($path);
    if ($value === false || $value === '' || strlen($value) > 1048576) {
        throw new RuntimeException('Required runtime Secret is unavailable');
    }
    return $value;
})();
