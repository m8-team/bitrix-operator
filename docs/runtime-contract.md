# Runtime contract v1 — итерация 2

Итерация добавляет чистый renderer `internal/runtime`, build-time подготовку release
и исполняемые проверки конфигураций. Reconciler пока сохраняет
`Ready=False / RuntimeNotImplemented`: Deployment, PVC и validation Job появляются
в следующих итерациях. API и RBAC не изменены.

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
xml/gd/zip/intl и исполнение настроек с кавычками, переводами строк и PHP-текстом
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
При старте будущий init container копирует `/opt/www/.` в отдельный EmptyDir;
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
COPY internal/runtime/assets/ /tmp/operator-assets/
COPY hack/runtime/prepare-release.sh /tmp/prepare-release.sh
RUN sh /tmp/prepare-release.sh /opt/www /tmp/operator-assets \
    && rm -rf /tmp/operator-assets /tmp/prepare-release.sh \
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
watch/rollout механизма следующей итерации; данный hash не решает ротацию.

| Данные | Размещение | Доступ |
|---|---|---|
| nginx.conf | отдельный ConfigMap | nginx |
| PHP manifest, helpers, php.ini, FPM | отдельный ConfigMap, `/run/bitrix-config` | PHP/cron/validation |
| DB/Redis password, crypto, license | projected Secret, `/run/bitrix-secrets` | PHP/cron/validation |
| license_key.php helper | mount поверх `/opt/www/bitrix/license_key.php` | только PHP |

Проекции обязательные (`optional=false`), mode 0440, с fsGroup 979 в будущем Pod.
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
TTL=0 допускается CRD, но явно отклоняется этим runtime contract.

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

## Проверка site IDs: контракт следующей итерации

Одноразовый Job запускает PHP CLI того же release/runtime. Запрос содержит cluster
UID, generation, resolved release digest, Job UID и запрошенные site IDs. Используется
минимальный bootstrap ядра и read-only вызов `Bitrix\Main\SiteTable::getList`, без
web prolog, агентов, сессий, installers и изменения БД. Нельзя считать произвольный
bootstrap read-only: перед подключением release нужны тесты побочных эффектов и
read-only DB session/role; пока это не доказано, `SitesValidated=False`.

Результат JSON записывается в termination message, до 4096 байт. Только protocol
version, identity, найденные IDs из запроса и фиксированный reason:
`Validated`, `SiteNotFound`, `ValidationUnavailable`. Пример:

```json
{"version":"v1","identity":{"clusterUID":"uid","generation":3,"release":"sha256:<64 hex>","attempt":"job-uid"},"sites":["s1"],"reason":"Validated"}
```

`ParseSiteResult` уже реализован и тестируется. Он отклоняет неизвестные поля,
лишний JSON, несовпадающую identity, недопустимые reason, дубли/посторонние site IDs,
неполный успешный результат и превышение размера. Контроллер дополнительно должен
проверять owner UID, Job UID, exit code и успешное завершение контейнера; логи
никогда не используются как результат. Число IDs следует разбивать на batch,
чтобы каждый ответ укладывался в лимит. Нельзя обрезать JSON.

Будущие параметры Job: backoffLimit=0, activeDeadlineSeconds=60,
automountServiceAccountToken=false, без Kubernetes RBAC, read-only rootfs,
TTL cleanup только после чтения результата. Timeout → `ValidationTimeout`, ошибка
старта/формата/подключения → `ValidationUnavailable`, отсутствующие IDs →
`SiteNotFound`; это reasons существующего Conditions, без новых CRD fields.
Смена generation, digest или Job UID аннулирует результат. Никакого pods/exec из
manager. Проверка существования ID не подтверждает привязку домена в настройках
самого Bitrix — отдельный acceptance test должен покрыть multi-site routing.

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
