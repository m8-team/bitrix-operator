# Native runtime configuration files

Конфигурации редактируются в родных форматах. В Go остаются типизированные данные,
проверки, функции безопасной подстановки и явный список выходных файлов.

| Файл | Назначение | Обработка |
|---|---|---|
| `nginx/nginx.conf.tmpl` | nginx HTTP/FastCGI, hosts, защита uploads | `text/template` |
| `php/php.ini.tmpl` | PHP options и временный каталог | `text/template` |
| `php/php-fpm.conf` | FPM global/pool configuration | побайтное копирование |
| `bitrix/settings.php` | PHP overlay connections/cache/session/crypto | побайтное копирование |
| `bitrix/settings-wrapper.php` | entry point для release settings | побайтное копирование |
| `bitrix/init-release.sh` | копирование release и подготовка writable mount targets | побайтное копирование |
| `bitrix/fpm-live.php` | локальная liveness PHP-FPM | побайтное копирование |
| `bitrix/ready.php` | readiness DB/Redis через settings wrapper | побайтное копирование |
| `bitrix/license_key.php` | чтение license из Secret file | побайтное копирование |
| `cron/scheduler.sh` | PID 1, graceful signals без PHP pcntl | побайтное копирование |
| `cron/scheduler.php` | duration scheduler, signals, heartbeat | побайтное копирование |
| `cron/agents.php` | shared filesystem lock и Bitrix agent bootstrap | побайтное копирование |
| `cron/health.php` | scheduler liveness/readiness | побайтное копирование |
| `cron/web-prepend.php` | отключение HTTP agents в FPM | побайтное копирование |
| `bitrix/settings-extra.php` | build-time placeholder дополнительного settings | только сборка release |
| `bitrix/license-placeholder.php` | build-time placeholder лицензии | только сборка release |

`config.go` включает файлы через `go:embed`; build-only placeholders намеренно
отсутствуют в выходном runtime Bundle. `manifest.json` — сериализованная Go-модель
данных и ссылок на Secret files для PHP, не замена конфигурационных файлов.
Содержимое Secrets никогда не передаётся шаблонизатору.

## Правила подстановки

- Только суффикс `.tmpl` включает `text/template`, с `missingkey=error`.
  Все остальные файлы копируются без интерпретации, включая PHP-переменные и `{{`.
- Внутри `.tmpl` синтаксис подстановки — `{{ ... }}`. Нативные nginx `$uri`,
  `$query_string` и другие переменные за его пределами остаются нетронутыми.
- `nginxHosts` проверяет DNS names, сортирует копию списка и соединяет имена.
  `nginxUploadsPattern` проверяет путь и экранирует метасимволы regex.
  `iniPath` проверяет разрешённый путь и заключает его в кавычки INI.
- `text/template` не выполняет контекстное экранирование автоматически.
  Новая подстановка требует отдельной проверки для целевого синтаксиса.
  Пустые обязательные значения отклоняются helper-функциями.
- Шаблоны — доверенные файлы репозитория; загрузка шаблонов из CR, произвольных
  ConfigMap, сети или пользовательского PHP-кода в manager не предусмотрена.
- Parse/execute/read errors завершают рендеринг целиком, без частичного Bundle.
  Статические файлы не нужно превращать в шаблоны ради единого расширения.

## Сопоставление с upstream

Ориентир — [bitrix-tools/env-docker, revision
46aff98f3e3d76a7f83fc50317dc3f6502a80865](https://github.com/bitrix-tools/env-docker/tree/46aff98f3e3d76a7f83fc50317dc3f6502a80865).
Файлы оператора написаны самостоятельно; это сопоставление решений, а не
включение исходных GPL-конфигураций upstream.

| Наши файлы | Upstream path | Отличия для Kubernetes |
|---|---|---|
| `nginx/nginx.conf.tmpl` | `confs/nginx/conf.d/default.conf`, `confs/nginx/inc/block.conf` | порт 8080, PHP на loopback, явные hosts, default reject, temp paths в `/tmp`; сложные DAV/intranet/Push rewrites пока не перенесены |
| `php/php-fpm.conf` | `confs/php82/etc/php-fpm.conf`, `confs/php82/etc/php-fpm.d/www.conf`, `docker.conf`, `zz-docker.conf` | единый самостоятельный config, foreground, ondemand, non-root, clear_env; pool и global остаются вместе для сохранения текущего mount contract |
| `php/php.ini.tmpl` | `confs/php82/etc/php/conf.d/bitrixenv.ini` | PHP baseline, Secret-safe diagnostics, временный путь из CR; development/debug и локальные socket settings не переносятся |
| `bitrix/*.php` | прямого аналога в выбранном snapshot нет | собственный release wrapper и Secret-file overlay; настройки сверяются с документацией Bitrix |

Источники Bitrix:
[settings](https://docs.1c-bitrix.ru/pages/framework/settings.html),
[database](https://docs.1c-bitrix.ru/pages/database/configuration.html),
[sessions](https://docs.1c-bitrix.ru/pages/framework/sessions.html),
[cache](https://docs.1c-bitrix.ru/pages/performance/caching.html).

## Добавление или изменение конфигурации

1. Добавить файл в каталог компонента; для нового компонента (например, Push или
   cron) добавить собственный каталог и embed pattern в `config.go`.
2. Для runtime-файла указать явное имя назначения и область доступа (nginx/PHP)
   в `renderConfigs`. Для build-only файла обновить build script и fixture.
3. Добавить только нужные поля typed model и проверенные helpers. Ссылки на Secrets
   остаются путями; значения читаются только соответствующим runtime-процессом.
4. Обновить Docker build context, если появился новый каталог, и provenance выше.
5. Проверить эталоны и native tools:

   ```sh
   go test ./internal/runtime
   docker build -f test/fixtures/runtime/Dockerfile -t bitrix-runtime-fixture .
   BITRIX_RUNTIME_CONTAINER_TESTS=1 go test -count=1 -v ./internal/runtime
   ```

Golden files в `../testdata/golden` первоначально зафиксированы до переноса 2.1;
в итерации 3 явно обновлены для probes/init helpers. Команда обновления:
`go run ./hack/update-golden`; каждый diff требует просмотра.
Проверяются все выходные файлы и hash Bundle для default/custom путей и hosts:
рефакторинг не должен вызывать лишний rollout. При намеренном изменении поведения
эталоны обновляются явно с просмотром diff; тесты никогда не перезаписывают их.
Сохранены `nginx -t`, `php-fpm -t`, `php -l`, HTTP tests и PHP semantic tests.
