<?php
// Installed at /opt/www/bitrix/.settings.php in the immutable release image.
declare(strict_types=1);
return (static function (): array {
    $base = require __DIR__ . '/.settings.release.php';
    $extra = is_file(__DIR__ . '/.settings_extra.release.php')
        ? require __DIR__ . '/.settings_extra.release.php' : [];
    if (!is_array($base) || !is_array($extra)) {
        throw new RuntimeException('Release settings must return arrays');
    }
    $render = require '/run/bitrix-config/settings.php';
    return $render($base, $extra);
})();
