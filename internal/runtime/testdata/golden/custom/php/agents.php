<?php
// Keep the shared lock open for the entire worker lifetime, including PHP shutdown functions.
// The storage backend must implement cross-node flock; RWX alone is not proof of that property.
$manifest = json_decode(file_get_contents(__DIR__ . '/manifest.json'), true, 512, JSON_THROW_ON_ERROR);
if (!$manifest['cron']['enabled'] || !$manifest['cron']['agents']) {
    exit(0);
}
$operatorAgentLock = fopen($manifest['cron']['uploadsPath'] . '/.bitrix-operator-agents.lock', 'c');
if ($operatorAgentLock === false) {
    exit(1);
}
if (!flock($operatorAgentLock, LOCK_EX | LOCK_NB)) {
    exit(75);
}
$_SERVER['DOCUMENT_ROOT'] = '/opt/www';
$DOCUMENT_ROOT = $_SERVER['DOCUMENT_ROOT'];
chdir($DOCUMENT_ROOT);
define('NO_KEEP_STATISTIC', true);
define('NOT_CHECK_PERMISSIONS', true);
define('BX_NO_ACCELERATOR_RESET', true);
define('CHK_EVENT', true);
define('BX_WITH_ON_AFTER_EPILOG', true);
set_time_limit(0);
require $DOCUMENT_ROOT . '/bitrix/modules/main/include/prolog_before.php';
// Required release/database preparation follows Bitrix's documented all-agents cron mode.
// The operator deliberately does not change persistent application options.
if (COption::GetOptionString('main', 'check_agents', 'Y') !== 'N'
    || COption::GetOptionString('main', 'agents_use_crontab', 'N') !== 'N'
    || (defined('BX_CRONTAB_SUPPORT') && BX_CRONTAB_SUPPORT)
    || (defined('BX_CRONTAB') && BX_CRONTAB)
    || (defined('NO_AGENT_CHECK') && NO_AGENT_CHECK)) {
    exit(1);
}
CAgent::CheckAgents();
// Email queues, sender campaigns and backups are outside the agents API contract.
