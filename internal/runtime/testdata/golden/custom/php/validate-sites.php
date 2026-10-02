<?php
// A bounded, machine-readable result only. Never send kernel output or exceptions to logs.
ini_set('display_errors', '0');
ini_set('log_errors', '0');
ob_start(static fn($output) => '', 8192);
$operatorResultPath = '/tmp/site-result.json';
$operatorIdentity = json_decode(getenv('BITRIX_VALIDATION_IDENTITY'), true, 32, JSON_THROW_ON_ERROR);
$operatorIdentity['attempt'] = getenv('BITRIX_VALIDATION_ATTEMPT');
$operatorResult = ['version' => 'v1', 'identity' => $operatorIdentity, 'sites' => [], 'reason' => 'ValidationUnavailable'];
$operatorWriteResult = static function (array $result) use ($operatorResultPath): void {
    $data = json_encode($result, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES);
    if (strlen($data) > 4096 || file_put_contents($operatorResultPath, $data, LOCK_EX) !== strlen($data)) {
        exit(1);
    }
};
// A fatal error, premature exit or OOM must leave an unavailable result, never partial success.
$operatorWriteResult($operatorResult);
try {
    $operatorManifest = json_decode(file_get_contents(__DIR__ . '/manifest.json'), true, 512, JSON_THROW_ON_ERROR);
    $_SERVER['DOCUMENT_ROOT'] = '/opt/www';
    $DOCUMENT_ROOT = $_SERVER['DOCUMENT_ROOT'];
    chdir($DOCUMENT_ROOT);
    define('NO_AGENT_CHECK', true);
    define('NO_KEEP_STATISTIC', true);
    define('NOT_CHECK_PERMISSIONS', true);
    define('BX_NO_ACCELERATOR_RESET', true);
    define('DisableEventsCheck', true);
    set_time_limit(90);
    define('BITRIX_OPERATOR_VALIDATION', true);
    // Basic D7 kernel only: no web prolog, init.php, agents, sessions or epilog.
    require $DOCUMENT_ROOT . '/bitrix/modules/main/lib/loader.php';
    \Bitrix\Main\Loader::registerNamespace('Bitrix\\Main', $DOCUMENT_ROOT . '/bitrix/modules/main/lib');
    \Bitrix\Main\Application::getInstance()->initializeBasicKernel();
    $operatorConnection = \Bitrix\Main\Application::getConnection();
    $operatorReadOnly = $operatorConnection->queryScalar($operatorManifest['engine'] === 'MySQL'
        ? 'SELECT @@session.transaction_read_only' : 'SHOW default_transaction_read_only');
    if (!in_array((string) $operatorReadOnly, ['1', 'on'], true)) {
        throw new RuntimeException('Read-only validation session required');
    }
    $operatorSites = [];
    foreach ($operatorManifest['sites'] as $operatorSiteID) {
        $operatorSiteRow = \Bitrix\Main\SiteTable::getList(['select' => ['LID'], 'filter' => ['=LID' => $operatorSiteID], 'limit' => 1])->fetch();
        if (is_array($operatorSiteRow) && ($operatorSiteRow['LID'] ?? null) === $operatorSiteID) {
            $operatorSites[] = $operatorSiteID;
        }
    }
    $operatorResult['sites'] = $operatorSites;
    $operatorResult['reason'] = count($operatorSites) === count($operatorManifest['sites']) ? 'Validated' : 'SiteNotFound';
    $operatorWriteResult($operatorResult);
} catch (Throwable $ignored) {
    // No exception text, database details or application output enter status or logs.
    exit(1);
}
