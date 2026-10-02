<?php
// FPM-only, before the application's bootstrap. Never writes global Bitrix DB options.
$operatorManifest = json_decode(file_get_contents('/run/bitrix-config/manifest.json'), true, 512, JSON_THROW_ON_ERROR);
if ($operatorManifest['cron']['enabled']) {
    define('NO_AGENT_CHECK', true);
}
// Read only the private FastCGI parameter. HTTP_BITRIX_SITE_ID is untrusted.
$operatorSite = $_SERVER['BITRIX_SITE_ID'] ?? '';
if ($operatorSite !== '') {
    if (!in_array($operatorSite, $operatorManifest['sites'], true)) {
        http_response_code(421);
        exit;
    }
    define('SITE_ID', $operatorSite);
}
unset($operatorManifest, $operatorSite);
