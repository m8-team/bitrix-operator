<?php
// Synthetic D7 test double. This is not Bitrix code or proof of compatibility with a licensed kernel.
namespace Bitrix\Main;
class Loader
{
    public static function autoLoad(string $class): void {}
    public static function registerNamespace(string $name, string $directory): void {}
}
class Application
{
    private static ?FixtureConnection $connection = null;
    public static function getInstance(): self { return new self(); }
    public function initializeBasicKernel(): void
    {
        if (!defined('NO_AGENT_CHECK') || !NO_AGENT_CHECK || !BITRIX_OPERATOR_VALIDATION) {
            throw new \RuntimeException('Unsafe bootstrap');
        }
        if (getenv('FIXTURE_SITE_MODE') === 'failure') {
            echo 'synthetic-secret-must-not-appear';
            throw new \RuntimeException('synthetic-secret-must-not-appear');
        }
        if (getenv('FIXTURE_SITE_MODE') === 'exit') { exit(0); }
        self::$connection = new FixtureConnection();
    }
    public static function getConnection(): FixtureConnection { return self::$connection; }
}
class FixtureConnection
{
    private mixed $db = null;
    private string $engine;
    public function __construct()
    {
        $m = json_decode(file_get_contents('/run/bitrix-config/manifest.json'), true, 512, JSON_THROW_ON_ERROR);
        $this->engine = $m['engine'];
        $settings = require '/opt/www/bitrix/.settings.php';
        $c = $settings['connections']['value']['default'];
        if ($c['options'] !== 0 || !str_contains($c['initCommand'], 'transaction_read_only')) {
            throw new \RuntimeException('Missing validation guard');
        }
        // Docker unit tests have no network; kind tests exercise both actual databases.
        if (getenv('FIXTURE_SITE_MODE') !== false) { return; }
        [$host, $port] = explode(':', $c['host'], 2);
        if ($this->engine === 'MySQL') {
            \mysqli_report(MYSQLI_REPORT_ERROR | MYSQLI_REPORT_STRICT);
            $this->db = new \mysqli($host, $c['login'], $c['password'], $c['database'], (int) $port);
            $this->db->query($c['initCommand']);
            try {
                $this->db->query('CREATE TABLE operator_validation_forbidden (id INT)');
                throw new \RuntimeException('Validation session can write');
            } catch (\mysqli_sql_exception $error) {
                if ($error->getCode() !== 1792) { throw $error; }
            }
        } else {
            $quote = static fn(string $v): string => "'" . str_replace(['\\', "'"], ['\\\\', "\\'"], $v) . "'";
            $dsn = 'host=' . $quote($host) . ' port=' . (int) $port . ' dbname=' . $quote($c['database'])
                . ' user=' . $quote($c['login']) . ' password=' . $quote($c['password']) . ' connect_timeout=2';
            $this->db = pg_connect($dsn, PGSQL_CONNECT_FORCE_NEW);
            if ($this->db === false || pg_query($this->db, $c['initCommand']) === false) { throw new \RuntimeException('Connection failed'); }
            pg_send_query($this->db, 'CREATE TABLE operator_validation_forbidden (id INT)');
            $result = pg_get_result($this->db);
            if (pg_result_error_field($result, PGSQL_DIAG_SQLSTATE) !== '25006') { throw new \RuntimeException('Validation session can write'); }
            while (pg_get_result($this->db) !== false) {}
        }
    }
    public function queryScalar(string $sql): mixed
    {
        if (getenv('FIXTURE_SITE_MODE') === 'writable') { return '0'; }
        if ($this->db === null) { return '1'; }
        return $this->engine === 'MySQL' ? $this->db->query($sql)->fetch_row()[0] : pg_fetch_row(pg_query($this->db, $sql))[0];
    }
}
class SiteTable
{
    public static function getList(array $parameters): object
    {
        if ($parameters['select'] !== ['LID'] || $parameters['limit'] !== 1) { throw new \RuntimeException('Unbounded query'); }
        $id = $parameters['filter']['=LID'];
        return new class($id) {
            public function __construct(private string $id) {}
            public function fetch(): array|false { return in_array($this->id, ['s1', 's2'], true) ? ['LID' => $this->id] : false; }
        };
    }
}
