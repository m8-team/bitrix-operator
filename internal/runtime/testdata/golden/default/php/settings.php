<?php
// Authored for bitrix-operator. No Secret values are emitted to disk or stdout.
declare(strict_types=1);

return static function (array $base, array $extra, string $directory = __DIR__): array {
    $readSecret = static function (string $path): string {
        if (!preg_match('~^/run/bitrix-secrets/[a-f0-9]{64}$~D', $path)) {
            throw new RuntimeException('Invalid runtime Secret path');
        }
        $value = @file_get_contents($path);
        if ($value === false || $value === '' || strlen($value) > 1048576) {
            throw new RuntimeException('Required runtime Secret is unavailable');
        }
        return $value; // Deliberately do not trim: whitespace is part of the password.
    };
    $manifest = @file_get_contents($directory . '/manifest.json');
    if ($manifest === false) {
        throw new RuntimeException('Runtime configuration unavailable');
    }
    $m = json_decode($manifest, true, 64, JSON_THROW_ON_ERROR);
    if (($m['version'] ?? '') !== 'v1') {
        throw new RuntimeException('Unsupported runtime contract');
    }
    // Bitrix overlays extra configuration at section level. Preserve every unmanaged section.
    $settings = array_replace($base, $extra);
    $connections = $settings['connections']['value'] ?? [];
    foreach ($m['connections'] as $name => $connection) {
        $connection['password'] = $readSecret($connection['passwordFile']);
        unset($connection['passwordFile']);
        // Fully replace managed connections: stale socket, server and replica settings must not survive.
        $connections[$name] = $connection;
    }
    $settings['connections'] = ['value' => $connections, 'readonly' => true];
    $redis = $m['redis'];
    $redis['password'] = $readSecret($redis['passwordFile']);
    unset($redis['passwordFile']);
    $cache = $settings['cache']['value'] ?? [];
    $cache['type'] = ['class_name' => '\\Bitrix\\Main\\Data\\CacheEngineRedis', 'extension' => 'redis'];
    $cache['redis'] = $redis;
    $cache['sid'] = $m['cacheSID'];
    $settings['cache'] = ['value' => $cache, 'readonly' => true];
    $session = $settings['session']['value'] ?? [];
    $session['mode'] = $m['sessionMode'];
    $session['lifetime'] = $m['sessionTTL'];
    // Current Bitrix Redis session handler uses the auth option.
    $handler = ['type' => 'redis', 'host' => $redis['host'], 'port' => $redis['port'], 'auth' => $redis['password']];
    $session['handlers'] = ['general' => $handler];
    if ($m['sessionMode'] === 'separated') {
        $session['handlers']['kernel'] = 'encrypted_cookies';
    }
    $settings['session'] = ['value' => $session, 'readonly' => true];
    $crypto = $settings['crypto']['value'] ?? [];
    $crypto['crypto_key'] = $readSecret($m['crypto']);
    $settings['crypto'] = ['value' => $crypto, 'readonly' => true];
    $pull = require $directory . '/pull.php';
    $settings['pull'] = ['value' => $pull($m['push'], $readSecret), 'readonly' => true];
    return $settings;
};
