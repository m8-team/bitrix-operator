# Runtime contract v1 — итерация 2

Итерация добавляет чистый renderer `internal/runtime`, build-time подготовку release
и исполняемые проверки конфигураций. В итерации 3 добавлены Deployment, Service,
retained PVC и dependency probes — см. [web workloads](web-workloads.md). API
расширен для site-validation и других runtime-ресурсов. Read-only D7
site-validation Job реализован в итерации 6.1, см. [Sites](sites.md).

## Родные файловые шаблоны — итерация 2.1

Конфигурации хранятся в `internal/runtime/templates/`: nginx — `.conf.tmpl`,
PHP — `.ini.tmpl`, PHP-FPM — статический `.conf`, Bitrix — статические `.php`.
Только `.tmpl` проходит через `text/template` с `missingkey=error`; остальные
файлы копируются побайтно. Go подготавливает типизированные данные, проверяет
параметры и применяет helpers для целевого формата. Значения Secrets остаются
в файлах runtime, а `manifest.json` передаёт PHP только данные и ссылки на них.

Полные конфигурации удалены из Go-строк. Golden tests фиксируют совпадение всех
выходных файлов и hash Bundle, включая custom hosts/paths. Перенос 2.1 не менял
результат; в итерации 3 эталоны явно обновлены для probe server и helper scripts. Build script также копирует родные
PHP placeholders вместо сборки PHP через shell. Структура, upstream provenance,
экранирование и добавление нового шаблона описаны в
[templates/README.md](../internal/runtime/templates/README.md).

## Образы и проверяемая совместимость

Источником структуры каталогов, PHP extensions и примеров служит
[env-docker](https://github.com/bitrix-tools/env-docker/tree/46aff98f3e3d76a7f83fc50317dc3f6502a80865).
Шаблоны оператора написаны самостоятельно; исходные GPL-конфигурации не включены.
OCI index digests получены из Quay 30 сентября 2026; оба индекса содержат linux/amd64
и linux/arm64. CI исполняет проверки на amd64. arm64 требует отдельного прогона.

| Компонент | Образ | OCI index digest |
|---|---|---|
| PHP | `quay.io/bitrix24/php:8.2.34-fpm-v1-alpine` | `sha256:58aa3acaaf6da1f38c2297abade120780be02afad99656f97c444fa3207ab440` |
| nginx | `quay.io/bitrix24/nginx:1.30.5-v1-alpine` | `sha256:77d042de08856d8644aad10c0bc744790e228b7837b76b2b8644159b073fcfb5` |

Версия PHP выбрана по baseline env-docker, а не по принципу максимального номера:
совместимость приложения важнее. Новые версии добавляются после тех же проверок.
CI проверяет `php -l`, `php-fpm -t`, `nginx -t`, наличие mysqli/pgsql/redis/mbstring/
xml/gd/zip/intl HTTP-проверку запрета служебных файлов и PHP в uploads,
и исполнение настроек с кавычками, переводами строк и PHP-текстом
в синтетических Secrets. Контейнеры работают под UID/GID 979, с read-only rootfs,
без capabilities, без сети и с `no-new-privileges`.

Redis — внешний сервис: оператор настраивает только клиентов, не сервер.
Настройки `protected-mode no`, политики eviction и другие dev-параметры env-docker
не переносятся. Cache SID должен быть уникален между независимыми установками;
Redis для сессий должен иметь подходящие доступность и политику сохранности данных.

[Официальная документация соединений](https://docs.1c-bitrix.ru/pages/database/configuration.html)
указывает поддержку Redis `password` с main 25.800.0. Это нижняя граница контракта,
не утверждение совместимости любой сборки Bitrix. Перед включением конкретного
лицензионного release обязательно проверить реальные подключения MySQL/PostgreSQL,
Redis cache и session handler (`auth`), создание/чтение сессии и site API.
Fixture не содержит ядра Bitrix, не выполняет SQL и не заменяет этот acceptance gate.

## Release image

Release содержит готовое приложение в `/opt/www`, `/bin/sh` и `cp`.
При старте init container копирует `/opt/www/.` в отдельный EmptyDir;
nginx и PHP используют одну копию. Доступ к коду read-only после инициализации.
В image не должно быть лицензии, паролей, production settings с секретами,
пользовательских uploads или дампов БД. Зависимости и обновления Bitrix собираются
заранее; web updater и автоматические DB migrations не входят в контракт.

`release.image.digest` имеет приоритет над `tag`; helper проверяет полный SHA256.
Для production закрепляйте digest и release, и runtime images. Тег остаётся
человеческой меткой. Tag-only разрешён API для разработки; site validation обязана
использовать resolved digest, иначе результат не считается подтверждённым.

Пример build (из корня репозитория; `app/` — подготовленное лицензированное приложение):

```dockerfile
FROM quay.io/bitrix24/php:8.2.34-fpm-v1-alpine@sha256:58aa3acaaf6da1f38c2297abade120780be02afad99656f97c444fa3207ab440
USER root
COPY app/ /opt/www/
COPY internal/runtime/templates/bitrix/ /tmp/operator-templates/
COPY hack/runtime/prepare-release.sh /tmp/prepare-release.sh
RUN sh /tmp/prepare-release.sh /opt/www /tmp/operator-templates \
    && rm -rf /tmp/operator-templates /tmp/prepare-release.sh \
    && chmod -R a+rX /opt/www
LABEL team.m8.bitrix-operator.runtime-contract="v1"
USER 979:979
```

Подготовка выполняется только на disposable build copy и повторный запуск завершается
ошибкой. `.settings.php` сохраняется как `.settings.release.php`, дополнительный
файл — как `.settings_extra.release.php`, в том же каталоге: `__DIR__` сохраняет
значение. Wrapper возвращает эффективные настройки с overlay оператора;
`.settings_extra.php` возвращает пустой массив, предотвращая повторное применение.
Оба исходных файла должны возвращать массивы без вывода, побочных эффектов и
рекурсивного подключения `.settings.php`. Включение этих файлов происходит в PHP,
никогда в manager. Лицензионный placeholder монтируется отдельным PHP-only файлом.

## Настройки и Secrets

`Render(spec)` возвращает независимые `Nginx`, `PHP`, projected Secret sources и
детерминированный hash конфигурации. Secret data не принимаются на вход, не
запрашиваются API-клиентом и не участвуют в hash. Изменение Secret требует отдельного
watch/rollout механизма: он реализован в итерации 3 отдельным hash metadata Secrets;
данный config hash не решает ротацию.

| Данные | Размещение | Доступ |
|---|---|---|
| nginx.conf | отдельный ConfigMap | nginx |
| PHP manifest, helpers, php.ini, FPM | отдельный ConfigMap, `/run/bitrix-config` | PHP/cron/validation |
| DB/Redis password, crypto, license | projected Secret, `/run/bitrix-secrets` | PHP/cron/validation |
| license_key.php helper | mount поверх `/opt/www/bitrix/license_key.php` | только PHP |

Проекции обязательные (`optional=false`), mode 0440, с fsGroup 979 в runtime Pod.
Имена файлов — SHA256 от пары Secret name/key: пользовательский key не становится
путём. Kubernetes проецирует Secrets в namespace самого BitrixCluster. Монтировать
нужно целый Secret volume, без `subPath`; PHP читает значения при загрузке настроек.
Не использовать переменные окружения, shell interpolation, содержимое ConfigMap,
status или логи для передачи secret data. Значения сохраняются побайтно, без trim.
При отсутствии, пустом или слишком большом значении PHP выдаёт общий текст ошибки.

JSON manifest экранирует строки независимо от синтаксиса PHP. Hostnames nginx
проверяются как lowercase DNS, wildcard и дубли между сайтами запрещены. SQL charset
и timezone ограничены безопасными токенами. MySQL получает единый `SET` через
`initCommand`; PostgreSQL — `SET client_encoding` и `SET TIME ZONE`. Имена БД и login
не вставляются в SQL. DB endpoint формируется как host:port для Bitrix connection.
IPv6 endpoints пока явно отклоняются: их разбор зависит от версии ядра.

Приоритет: base → extra (по секциям, как в Bitrix) → управляемые поля оператора.
Все сторонние секции и неуправляемые соединения сохраняются. Управляемое именованное
соединение заменяется целиком; также заменяются `cache.type/redis/sid`,
`session.mode/lifetime/handlers`, `crypto.crypto_key`. Остальные параметры этих
секций, например `cache.use_lock` и `session.regenerateIdAfterLogin`, сохраняются.
Управляемые секции помечаются readonly. `default` и `separated` поддерживаются;
TTL=0 отклоняется admission validation CRD и runtime contract.

nginx слушает 8080, PHP-FPM — только loopback:9000. Неизвестный Host закрывается;
скрытые/служебные файлы и PHP в uploads запрещены. PHP выполняется только для
существующих `.php`, прочие PHP-подобные расширения запрещены. Основной fallback —
`/bitrix/urlrewrite.php`. Это базовый web contract; DAV, intranet rewrites, Push,
доверенные proxy headers и HTTPS cookies потребуют тестов следующих итераций.
Служебный nginx endpoint не означает готовности приложения или БД.

Uploads разрешены ниже `/opt/www`, вне `bitrix`/`local`; temporary — ниже `/opt`,
вне document root. Иные пути отклоняются до создания workloads. Стандартные пути:
`/opt/www/upload`, `/opt/.bx_temp`. PHP cache/temp writable mounts необходимо
согласовать с конкретным release до запуска приложения. FPM defaults (5 процессов,
512M memory_limit) — baseline, не автоматическое вычисление из Pod resources.

## Проверка site IDs

Реализована в итерации 6.1: [Sites — запуск, protocol, retries и ограничения](sites.md).
PHP CLI загружает Loader и basic D7 kernel, использует read-only DB session и
`Bitrix\Main\SiteTable::getList`; web prolog и agents не запускаются.
Нативные файлы `bitrix/validate-sites.php`, `bitrix/settings.php` и
`cron/web-prepend.php` входят в versioned ConfigMap. Публичный FastCGI запрос
получает SITE_ID по таблице Host из CR; входной HTTP заголовок его не выбирает.

JSON termination message ограничен 4096 байт. Только protocol version, identity,
найденные IDs из запроса и фиксированный reason. Identity содержит cluster UID,
generation, наблюдаемый image digest и Job UID; имя Job включает также config hash
и Secret revision. Пример:

```json
{"version":"v1","identity":{"clusterUID":"uid","generation":3,"release":"sha256:<64 hex>","attempt":"job-uid"},"sites":["s1"],"reason":"Validated"}
```

`ParseSiteResult` отклоняет неизвестные поля, лишний JSON, несовпадающую identity,
неверные reasons, дубли/посторонние IDs и неполный результат. Контроллер проверяет
owner UID, завершение Job/контейнера и фактический release imageID его init container.
Логи и pods/exec не используются. Большой запрос пока получает ResultTooLarge;
разбиение на batch остаётся отдельным улучшением, JSON никогда не обрезается.
Настоящий лицензированный Bitrix smoke по-прежнему обязателен перед production.

## Запуск проверок

```sh
go test ./internal/runtime
docker build -f test/fixtures/runtime/Dockerfile -t bitrix-runtime-fixture .
BITRIX_RUNTIME_CONTAINER_TESTS=1 go test -count=1 -v ./internal/runtime
make lint-fix
make test
make verify-generated
```

Без opt-in Docker-тест пропускается локально; отдельный обязательный workflow
Runtime contract включает opt-in и падает при отсутствии Docker или runtime tools.
Fixture — синтетическое приложение без лицензии/кода Bitrix. Полный запуск Bitrix,
внешние DB/Redis, Gateway и Kubernetes workloads этим тестом не проверяются.

Дополнительные первоисточники:
[settings](https://docs.1c-bitrix.ru/pages/framework/settings.html),
[sessions](https://docs.1c-bitrix.ru/pages/framework/sessions.html),
[cache](https://docs.1c-bitrix.ru/pages/performance/caching.html),
[MySQL API](https://docs.1c-bitrix.ru/api/classes/Bitrix-Main-DB-MysqliConnection.html),
[PostgreSQL API](https://docs.1c-bitrix.ru/api/classes/Bitrix-Main-DB-PgsqlConnection.html).
