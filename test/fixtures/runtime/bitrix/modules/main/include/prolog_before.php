<?php
// Synthetic test double, NOT a Bitrix kernel. Never included in a production release.
class COption
{
    public static function GetOptionString($module, $name, $default): string { return 'N'; }
}
class CAgent
{
    public static function CheckAgents(): void
    {
        $root = '/opt/www/upload';
        if (!mkdir($root . '/active', 0700)) {
            file_put_contents($root . '/overlap', 'failure');
            throw new RuntimeException('overlap');
        }
        file_put_contents($root . '/starts', hrtime(true) . "\n", FILE_APPEND);
        // Deliberately longer than the test scheduler interval.
        usleep(1600000);
        rmdir($root . '/active');
        file_put_contents($root . '/ends', hrtime(true) . "\n", FILE_APPEND);
        if (is_file($root . '/fail')) {
            throw new RuntimeException('synthetic-secret-must-not-appear');
        }
    }
}
