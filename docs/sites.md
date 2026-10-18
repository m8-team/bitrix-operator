# Sites (итерация 6.1)

`sites[].hosts` определяет таблицу nginx Host → `sites[].id`. Все сайты используют
общий document root, code release и uploads. Для PHP nginx передаёт отдельный
FastCGI параметр `BITRIX_SITE_ID`, а FPM auto_prepend определяет `SITE_ID` до
bootstrap приложения. HTTP-заголовок `Bitrix-Site-ID` не используется. Это host-based
multi-site; переключение сайтов по URL-папкам пока не поддерживается. Неизвестный
Host закрывается, один Host не может принадлежать двум site IDs внутри CR.

## Проверка и готовность

После WebReady оператор наблюдает готовые web Pods и проверяет цепочку владельцев
Deployment → ReplicaSet → Pod, config hash, Secret revision и release/runtime images.
Он получает фактический release digest из успешного init container, в том числе для
tag-only CR. Пока digest не определён или отличается между репликами,
`SitesValidated=Unknown/ReleaseUnresolved`. Смешанные архитектуры с разными runtime
imageID сейчас требуют отдельного решения; такой rollout не получает ложный Ready.

Validation Job использует тот же release image и PHP runtime, `restartPolicy: Never`,
`backoffLimit: 0`, deadline 120 секунд, UID 979, read-only rootfs и отключённый токен
ServiceAccount. Uploads заменены EmptyDir: Job не подключает и не изменяет пользовательский
PVC. Init container не получает credentials. Labels Job исключают его из web Service
и из Push publisher allowlist. При default-deny egress инфраструктура должна разрешить
Pod с `app.kubernetes.io/name=bitrix-sites` доступ к DNS и внешней БД.

Native PHP загружает `main/lib/loader.php`, регистрирует namespace `Bitrix\\Main`,
вызывает `Application::initializeBasicKernel` и читает `SiteTable` с точным фильтром
LID. Web prolog, пользовательский init.php, agents и epilog не подключаются.
Settings overlay включает read-only DB session до запросов ядра: MySQL
`transaction_read_only=1`, PostgreSQL `default_transaction_read_only=on`. В validation
отключены persistent connections и убраны unmanaged connections. Скрипт проверяет
read-only режим основной сессии перед чтением; невозможность проверки даёт
ValidationUnavailable. Настройки web/cron и глобальные DB options не изменяются.
Это защита сеанса, а не песочница для недоверенного release-кода: приложение,
которое самостоятельно открывает connections или снимает read-only, вне контракта.

JSON пишется только в `/tmp/site-result.json` (termination message, ≤4096 байт).
Kernel output и exception messages не являются результатом. Контроллер принимает
только успешный принадлежащий Job Pod с exit code 0, совпадающим imageID и полной
identity. Старый результат после изменения generation, конфигурации или Secrets
не подтверждает новый rollout. Изменяемый tag с отличающимся imageID в Job также
отклоняется; для production используйте digest-pinned releases.

| Condition reason | Значение |
|---|---|
| ValidationPending | Ожидание текущего Job или удаления предыдущего |
| RuntimeUnresolved | Не подтверждён единый imageID готовых web Pods |
| Validated | Все запрошенные site IDs существуют |
| SiteNotFound | Есть отсутствующие IDs; существующие отмечены в status.sites |
| ValidationUnavailable | Ошибка bootstrap/DB, формата, owner/identity или контейнера |
| ValidationTimeout | Job превысил deadline |
| ResultTooLarge | Ответ со всеми IDs не поместится в termination message |

Успешная проверка привязана к generation, hash конфигурации, ревизии Secrets,
resolved release/PHP digests и UID Job. Контроллер сохраняет в
`status.siteValidation` identity Job, его UID и `validatedAt` (время завершения
успешной проверки). Это подтверждение переживает рестарт manager и исчезновение
завершённого Pod; оно не переиспользуется при смене runtime identity.

**Freshness policy:** успешная проверка действительна 15 минут. По истечении TTL
оператор foreground-удаляет завершённый Job и создаёт новый с тем же
детерминированным именем, но новым UID. Во время обычного refresh он сохраняет
`SitesValidated=True` и предыдущие `status.sites[].exists`, **только если**
результат проверен для текущего runtime и моложе 30 минут. Ошибка новой проверки
сразу устанавливает `SitesValidated=False`; если подтверждение старше 30 минут,
ожидание новой проверки устанавливает `SitesValidated=Unknown/ValidationStale`
и снимает флаги exists. Идентичность и время проверяются при каждом reconcile.
Неуспешный Job повторяется после пяти минут.

При исправно работающем controller-runtime следующий reconcile запускается к
границе 15-минутного TTL (а не только по резервному ресинку 10–12 минут).
У validation Job есть deadline 120 секунд, поэтому обычное изменение SiteTable
обнаруживается примерно за 15–18 минут. Это эксплуатационная цель, а не гарантия
при сбоях API, длительной очереди или недоступности DB. Допустимое сохранение
предыдущего True ограничено 30 минутами от `validatedAt`; при сбое reconcile
этот срок не может быть гарантирован без нового запуска controller.

При изменении generation, Secrets или digest новая identity сразу сбрасывает
предыдущее доказательство и ждёт новый validation Job. Старые Jobs удаляются
foreground с UID/resourceVersion preconditions; отсутствие Pod после уже
проверенной успешной попытки не вызывает искусственный NotReady.
Для TLS certificate expiry используется отдельная проверка при Gateway
reconciliation; максимальная задержка очередного healthy resync — 12 минут.

`Ready=True` требует текущих StorageReady, WebReady, CronReady, PushReady и
SitesValidated. Отключённые Cron/Push удовлетворяют своим Conditions как Disabled.
Без Gateway `status.sites[].urls` пуст: наличие site ID не означает публичного URL.
Gateway API, TLS, конфликты hosts между BitrixCluster и публикация URLs — [часть 6.2](gateway-tls.md).
Admission-валидация CRD отвергает wildcard и IP hosts, заглавные буквы, неверные DNS-1123 метки и совпадения hostname между разными site IDs. Максимум 64 site IDs (каждый до 32 символов) и 32 DNS names на сайт; termination message ограничен 4096 байт. Runtime renderer продолжает независимо проверять те же инварианты.

## Проверки и пределы доказательств

Envtest проверяет owner/identity, stale generation, Secret rotation, image mismatch,
exit code, expiry и no-op reconcile. Native Docker tests проверяют nginx/PHP и
bounded JSON, подавление сообщений ошибок и отказ при writable session. Kind
проверяет оба настоящих DB engine: validation session отвергает попытку DDL,
проверяет routing двух сайтов/alias и spoofed header, missing ID и восстановление.

Bitrix D7 в fixture синтетический; он не доказывает совместимость bootstrap с любой
версией лицензированного ядра. Перед production нужен smoke конкретного release:
реальный SiteTable, отсутствие побочных эффектов, работа basic kernel и read-only
connections. Несовместимый bootstrap должен оставлять SitesValidated=False.

Источники: [Loader](https://dev.1c-bitrix.ru/api_d7/bitrix/main/loader/index.php),
[initializeBasicKernel](https://dev.1c-bitrix.ru/api_d7/bitrix/main/application/initializebasickernel.php),
[SITE_ID и порядок исполнения](https://dev.1c-bitrix.ru/api_help/main/general/pageplan.php).
