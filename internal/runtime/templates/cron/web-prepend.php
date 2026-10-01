<?php
// FPM-only, before the application's bootstrap. Never writes global Bitrix DB options.
$operatorManifest = json_decode(file_get_contents('/run/bitrix-config/manifest.json'), true, 512, JSON_THROW_ON_ERROR);
if ($operatorManifest['cron']['enabled']) {
    define('NO_AGENT_CHECK', true);
}
unset($operatorManifest);
