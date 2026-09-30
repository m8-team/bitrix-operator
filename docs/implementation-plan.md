# План разработки Bitrix Operator

Дата проверки источников: 2026-09-29. Статус: итерация 1 — API foundation; см. docs/foundation.md.
Репозиторий: https://github.com/m8-team/bitrix-operator
Go module: github.com/m8-team/bitrix-operator

## 1. Цель и исходный контракт

Оператор управляет коробочным корпоративным порталом и сайтами Bitrix в Kubernetes.
Единственный пользовательский API — предоставленный BitrixCluster v1alpha1.
Исходный файл хранится без изменений в docs/reference/bitrixcluster_types.go.txt.
Новые поля и дополнительные пользовательские CRD в рамках этого плана не вводятся.

Go-файл содержит API types и kubebuilder markers, но не готовый установленный CRD:
OpenAPI schema, deepcopy и RBAC будут сгенерированы в итерации 1.
API group в файле не указан. Предлагаемый group — bitrix.m8.team;
его фиксация в groupversion_info.go и PROJECT — часть итерации 1.
Не объявлять API group существующим фактом.

БД и Redis являются внешними зависимостями: оператор настраивает подключения,
но не создает их серверы, не обновляет их и не управляет резервными копиями БД.
Gateway и TLS Secrets предоставляются инфраструктурой.
Один BitrixCluster — одна установка Bitrix, несколько существующих sites внутри нее.
Оператор не создает сайты в БД и не устанавливает лицензионный продукт из интернета.

## 2. Проверенная технологическая база

| Компонент | База для реализации | Правило |
| --- | --- | --- |
| Kubebuilder | v4.16.0, stable release от 2026-09-10 | Проверить latest перед scaffold, затем закрепить конкретную версию |
| Plugin | go/v4 | Стандартный scaffold и PROJECT с cliVersion |
| Go | baseline 1.26.0 в scaffold v4.16.0 | Выбрать актуальный совместимый patch при реализации и закрепить toolchain |
| controller-runtime | v0.25.0 в scaffold | Обновлять совместно с Kubernetes Go dependencies |
| k8s.io/* | v0.37.0 в scaffold | Версия библиотек не равна обещанной минимальной версии кластера |
| controller-tools | v0.22.0 в release notes | Воспроизводимые make generate / make manifests |
| Gateway API | HTTPRoute и Gateway из stable v1 API | Версию Go module и CRD bundle закрепить после compatibility check |
| Доставка | Kustomize сначала; Helm в release-итерации | Одинаковые CRD и security defaults |
| Тесты | Go unit tests, envtest, kind E2E | Реальный Bitrix — отдельный smoke test с предоставленным образом |

Матрицу поддерживаемых Kubernetes/Gateway/PHP/Bitrix версий публикуем только после тестирования.
Не использовать плавающие latest-теги в CI и опубликованных примерах.
Не включать alpha Kubebuilder plugins только ради новизны; SSA применяется как
механизм Kubernetes с явным владением полями. Alpha SSA scaffolding не обязателен.

Источники:
- https://github.com/kubernetes-sigs/kubebuilder/releases/tag/v4.16.0
- https://github.com/kubernetes-sigs/kubebuilder/blob/v4.16.0/testdata/project-v4/go.mod
- https://book.kubebuilder.io/
- https://book.kubebuilder.io/reference/server-side-apply
- https://gateway-api.sigs.k8s.io/guides/tls/

## 3. Архитектура

Контроллер использует схему observe → validate → build desired resources → apply →
observe readiness → patch status. Reconcile идемпотентный, повторяемый после
перезапуска, без длительных операций и shell-команд внутри процесса manager.
Чистые Go builders рендерят ресурсы и конфиги; Kubernetes client вынесен в слой reconcile.

Планируемая структура после scaffold:

```text
api/v1alpha1/                 # предоставленные API types, groupversion, deepcopy
cmd/main.go                  # manager, schemes, probes, leader election
internal/controller/         # reconcile, watches, dependency indexing
internal/resources/          # typed resource builders, ownership/apply
internal/render/             # nginx, PHP, Bitrix settings, push, cron
internal/validation/         # runtime validation без изменения API
internal/status/             # Conditions, phase, observed generation
internal/runtime/            # контракт release image и helper workloads
config/                      # CRD, RBAC, manager, Kustomize, samples
test/e2e/                    # kind lifecycle и failure scenarios
docs/                        # план, ADR, установка, эксплуатация
```

### Соответствие CRD и ресурсов

| Поле | Реализация | Проверка |
| --- | --- | --- |
| release.image | Init container с application release; digest имеет приоритет | Каждый новый Pod использует один и тот же pinned release |
| release.imagePullSecrets | PodSpec imagePullSecrets для использующих их workloads | Приватные образы доступны без раскрытия credentials |
| license / crypto | Projected Secret keys, runtime-generated PHP config | Значения отсутствуют в ConfigMap, status, Events и логах |
| sites | Host routing nginx, HTTPRoute на site, проверка site ID через Bitrix | Неизвестный site не получает Exists=true |
| runtime.web | Deployment: nginx + PHP-FPM, ClusterIP Service | PHP через localhost, readiness и rollout |
| runtime.web.autoscaling | HPA autoscaling/v2 | Контроллер не переписывает replicas, которыми владеет HPA |
| runtime.cron | Deployment, replicas=1, Recreate, scheduler wrapper | Нет overlap на штатном rollout; дополнительно блокировка исполнения |
| runtime.push | Отдельные publisher/subscriber Deployments и Services | Redis, подпись, websocket и независимое масштабирование |
| database.connections | Именованные подключения в Bitrix settings | Сохраняются name, engine, port, options; ровно одно имя default |
| redis | Настройки Bitrix cache и sessions | SID, mode, TTL, пароль, совместимость |
| storage.uploads | PVC, общий volume для PHP/nginx/cron | Сохранение данных, access modes, расширение |
| storage.temporary | EmptyDir на Pod с заданным sizeLimit | Изоляция Pod и правильные пути |
| networking.gateway | HTTPRoute к существующему listener | Accepted, ResolvedRefs, hostname, allowedRoutes, TLS |
| status | Conditions + phase + release + sites + runtime | Ready отражает наблюдаемое состояние текущей generation |

Secret, ConfigMap, Deployment, PVC, HPA и Route events должны приводить к reconcile.
Для внешних Secrets/Gateway — индексы зависимостей и MapFunc, не owner reference.
Generation predicate не должен отбрасывать обновления зависимостей и дочернего status.
Повторные status patches выполняются только при изменении; transition time сохраняется.
Transient errors используют backoff, ожидания — RequeueAfter; неверная конфигурация
отражается Condition с понятным reason без бесконечного горячего цикла.

### Release image и файловая система

Предлагаемый image contract: release image содержит готовый код в /opt/www и
минимальный copy entrypoint. Init container копирует код в отдельный EmptyDir Pod;
nginx/PHP/cron используют одинаковый digest и одинаковую структуру путей.
Не монтировать изменяемый общий PVC поверх всего кода приложения: только uploads.
Не смешивать код разных releases на общей файловой системе.

В итерации 2 зафиксировать точные writable paths, UID/GID, PHP extensions, symlinks,
license/config locations, .settings.php/.settings_extra.php integration и
сохранение custom settings. Секретные runtime-файлы генерируются в ограниченном
volume, доступном PHP/cron; nginx получает только нужный код и статику.
Не выдавать nginx доступ к license, crypto и DB credentials без необходимости.

Код релиза не обновляется через web updater: изменение Bitrix/modules входит в новый
application image. Миграции схемы БД — отдельная процедура владельца приложения
с backup и совместимостью старой/новой версии. Автоматический rollback образа
не является rollback БД. Оператор не запускает разрушительные миграции.

Первое получение digest при tag-only release нужно отдельно определить и проверить:
не превращать изменяемый tag в ложную гарантию immutable release и не придумывать digest
в status. Для production рекомендовать explicit digest; tag-only остается допустимым
по исходному API.

### Хранилище и безопасность данных

Uploads PVC сохраняется при удалении BitrixCluster: не ставить на него удаляющий
ownerReference. Хранить метки и UID исходного владельца; при коллизии имени не
подхватывать чужой/старый PVC автоматически. Восстановление — документированная
явная процедура, без новых полей spec.

PVC resize разрешается только вверх и при поддержке StorageClass. Изменения
storageClass/accessModes и shrink не приводят к удалению/пересозданию данных.

Несколько web Pod, cron и rolling update требуют совместимого shared storage.
ReadWriteOnce допускает несколько Pod на одном узле, но не гарантирует работу
распределенного deployment; ReadWriteOncePod несовместим с web + cron.
ReadOnlyMany не подходит для записи uploads. Production baseline — ReadWriteMany.
Неподдерживаемое сочетание полей выявляется до запуска rollout с конкретным reason.
Режим одного web Pod без cron на RWO допустим только после проверки стратегии
Recreate и CSI; нельзя молча обещать HA.

### Cron и агенты

Один replica и Recreate предотвращают штатный overlap rollout, но не гарантируют
единственность исполнения при сетевом разделении или принудительном удалении Pod.
Нужна блокировка на уровне БД/Bitrix для каждого запуска с безопасным отказом
при потере соединения; протокол для MySQL/PostgreSQL проверяется отдельно.
Не заявлять exactly-once: агенты должны выдерживать повторное выполнение.

Interval — metav1.Duration, поэтому Kubernetes CronJob с минутным cron-синтаксисом
не является общим решением. Использовать scheduler wrapper с duration, корректной
обработкой SIGTERM, timeout и без параллельных запусков.
runtime.cron.enabled и agents.enabled имеют отдельную семантику; отключенные агенты
не запускаются. Автозапуск агентов на web hits отключается согласованно с cron-режимом.

### Gateway и TLS

TLS certificateRefs принадлежат Gateway listener, а не HTTPRoute.
Оператор создает Routes, читает Gateway и проверяет соответствие sites[].tls
сертификату listener. Общий Gateway не изменяется автоматически.
Для HTTPS требуется sites[].tls и существующий kubernetes.io/tls Secret; для HTTP TLS
не обязателен. При networking absent/disabled не создавать Gateway API resources.

При Gateway в другом namespace инфраструктура настраивает certificateRefs с namespace
и ReferenceGrant в namespace сертификата, а также allowedRoutes.
Одного ReferenceGrant недостаточно без certificateRef. Не копировать private key
в другой namespace. Проверить SAN/срок сертификата, listener hostname, тип протокола,
Accepted/ResolvedRefs и наблюдаемую generation Route.
Несколько site certificates на одном listener требуют поддержки Gateway implementation
и явной настройки certificateRefs; несовместимость обозначается Condition.

Без Gateway CRDs оператор продолжает обслуживать внутренние кластеры; запрос Gateway
publication сообщает DependencyMissing. Не делать безусловный watch отсутствующего GVK,
который не даст manager запуститься. Поведение установки CRD после старта
(динамическая регистрация либо документированный restart) проверить отдельно.

### Secrets, безопасность и эксплуатация

- Least-privilege RBAC; application Pods без API token, кроме строго ограниченного
  helper при необходимости. Operator service account отделен от workloads.
- Secret references только в namespace BitrixCluster; проверять name/key и
  optional-флаг, не допускать молчаливый старт без обязательного значения.
- Secret rotation запускает rollout зависимых workloads. В публичных annotations
  использовать идентификаторы версий, а не значения или хеши слабых паролей.
- Не логировать spec целиком, конфиги с credentials или stdout приложения с секретами.
- Non-root, seccomp RuntimeDefault, drop capabilities, readOnlyRootFilesystem
  после проверки совместимости конкретных upstream images; nginx на непривилегированном порту.
- NetworkPolicy должна учитывать DNS, Gateway controller и реальные DB/Redis endpoints.
  Не обещать FQDN filtering стандартной NetworkPolicy; egress-настройка окружения —
  deployment overlays, а не выдуманные поля CRD.
- Liveness не зависит от доступности внешней БД/Redis; readiness может учитывать
  работоспособность приложения с ограниченными timeout.
- PDB и topology spread согласуются с replicas, storage и возможностью drain.
- Leader election, graceful shutdown, secure metrics, healthz/readyz, structured logs.
- Finalizers только для реального внешнего cleanup; не задерживать удаление CR
  ради обычных owner-managed Kubernetes resources.

## 4. Использование bitrix-tools/env-docker

Проверенный snapshot:
46aff98f3e3d76a7f83fc50317dc3f6502a80865

Постоянная ссылка:
https://github.com/bitrix-tools/env-docker/tree/46aff98f3e3d76a7f83fc50317dc3f6502a80865

| Источник | Что использовать | Что адаптировать |
| --- | --- | --- |
| docker-compose.yml | Images, volumes, команды, разделение pub/sub | Kubernetes Services/DNS, probes, securityContext |
| confs/nginx/conf.d/default.conf | /opt/www, urlrewrite.php, FastCGI, Bitrix locations | server_name по hosts, localhost PHP, TLS на Gateway |
| confs/nginx/inc/* | static, block, DAV, intranet, push routes | Совместимость портала/сайтов и безопасная публикация |
| confs/php82/etc/php/conf.d/bitrixenv.ini | Базовые настройки PHP/Bitrix | Memory limits, uploads, writable paths, production logging |
| confs/php82/etc/php-fpm.d/* | FPM pool и Docker overrides | Оценивать итог всех include, не один www.conf |
| .env_push_pub / .env_push_sub | pub=9010, sub=8010, роли | Secret refs, Services, replicas |
| confs/redis/redis.conf | Рекомендации и исходные параметры | Только эксплуатационный пример для внешнего Redis |

В snapshot docker-compose указаны nginx quay.io/bitrix24/nginx:1.30.5-v1-alpine,
PHP quay.io/bitrix24/php:8.2.34-fpm-v1-alpine,
push quay.io/bitrix24/push:3.3-v1-alpine, Redis redis:8.2.10-alpine.
Это прочитанные references, а не проверенная доступность registry или сертифицированная
матрица совместимости. Pull, digest, архитектуры, entrypoint и лицензии проверяются
в итерации 2. Новейший PHP не становится default без проверки версии Bitrix.

Redis example содержит protected-mode no, appendonly no и allkeys-lru.
Эти настройки не переносятся автоматически: при общем Redis вытеснение затрагивает
сессии и другие ключи, а logical databases не изолируют eviction policy.
Нужны namespace keys, совместимость session locking, TTL и документированный выбор
политики памяти с тестами поведения при OOM/eviction.
Сервер Redis остается ответственностью инфраструктуры.

Перед переносом upstream файлов проверить LICENSE/NOTICE и сохранить attribution.
Не копировать sample сертификаты, private keys и placeholder passwords.
Безопасность /bitrix/pub/ и /bitrix/rest/ проверяется по протоколу Push; их наличие
в примере не является основанием для бесконтрольной публичной публикации.

## 5. Итерации и критерии готовности

Каждая итерация: краткая спецификация → ADR при существенном решении → код →
проверки → отдельная ветка/PR → обновление статуса плана.
Первый commit пустого репозитория содержит этот план и исходный API.
Дальнейшие ветки: feat/01-foundation, feat/02-runtime-contract и т.д.
Без force push и автоматического merge незавершенных итераций.

### 0. План и baseline — завершено

- [x] Прочитать весь предоставленный API.
- [x] Проверить состояние репозитория и доступ.
- [x] Проверить latest stable Kubebuilder и reference configs.
- [x] Описать ресурсы, lifecycle, риски, границы владения.
- [x] Сохранить baseline в GitHub (контроль чтением после публикации).

Результат: reviewable план; работающий controller еще не заявляется.

### 1. Foundation: Kubebuilder и API — реализовано в feat/01-foundation

- [x] Создать проект настоящим Kubebuilder CLI, проверить checksum бинарника.
- [x] Зафиксировать versions, PROJECT, Go module, license, стандартный Makefile.
- [x] Перенести исходный API в api/v1alpha1 без изменения spec/status и JSON names.
- [x] Сгенерировать deepcopy, CRD, RBAC и начальный controller.
- [x] Проверить CEL/defaults на реальном envtest API server, включая optional TLS,
  имя default connection, optional objects, omitzero и duration.
- [x] CI: gofmt, vet, lint, unit/envtest, generated-file drift.
- [x] Зафиксировать Kubernetes test matrix и version provenance.

Готовность: build/test проходят; CRD устанавливается; HTTP sample без TLS принимается;
неверные enum/required/CEL отклоняются; повторная генерация не дает diff.

### 2. Runtime contract и конфигурации Bitrix

- Зафиксировать release-image contract и совместимые runtime images/digests.
- Реализовать renderer nginx, PHP-FPM, Bitrix connections/cache/sessions/license/crypto.
- Проверить чтение Secret files и безопасное экранирование PHP/nginx values.
- Подготовить контролируемый test fixture image и пользовательский image build guide.
- Определить проверку site IDs через Bitrix API внутри PHP runtime:
  одноразовый read-only validation Job с ограниченным JSON result
  (например, termination message), без shell exec из manager и без изменения БД.
  Результат связывается с generation/release; timeout и невозможность проверки —
  явные Conditions. Размер результата ограничен и проверяется.

Готовность: nginx -t, php -l, конфигурационные tests; сложные пароли не ломают
синтаксис и не попадают в публичные объекты; существующие settings не теряются.

### 3. Первый вертикальный срез: web и storage

- PVC retention, collision handling, безопасный resize.
- Release init container, web Deployment nginx/PHP, Service, конфигурации.
- Подключение MySQL/PostgreSQL и Redis, Secret dependency watches.
- Conditions и runtime/release/sites status, probes.
- Работа внутреннего HTTP без установленного Gateway API.

Готовность: CR приводит к рабочему web fixture; изменение конфигурации приводит
к rollout; удаленный дочерний ресурс восстанавливается; повторный reconcile не
создает diff; uploads сохраняется после удаления CR.
Реальная функциональность Bitrix подтверждается отдельным smoke test.

### 4. Cron и agents

- Singleton Deployment Recreate, image PHP из runtime.web.php.
- Duration scheduler, graceful stop и стратегия блокировки исполнения.
- Согласованные настройки агентов web/cron.
- Удаление owned cron workload при enabled=false.

Готовность: тесты interval, agents=false, смены release и manager restart;
нет одновременного исполнения в проверенных failure scenarios; ограничения
exactly-once и split-brain документированы.

### 5. Push & Pull

- Publisher/subscriber Deployments/Services, Secret, Redis.
- nginx websocket/long polling/protocol routes и app settings.
- Раздельное масштабирование, probes, отключение и очистка owned resources.

Готовность: подписка получает событие; проверяется подпись, reconnect,
масштабирование и отсутствие публичного неавторизованного publish.

### 6. Sites, Gateway API и TLS

- Multi-site host routing и site existence validation.
- HTTPRoute stable API; HTTP и HTTPS-сценарии.
- Listener/certificate/ReferenceGrant checks без изменения общего Gateway.
- Watches зависимостей и корректные URLs/status.
- Удаление устаревших Routes при удалении site или отключении publication.

Готовность: HTTP без TLS; HTTPS с TLS; отклонение несовместимого listener;
cross-namespace кейс; certificate rotation; отсутствие Gateway CRDs;
неизвестный site и конфликты hosts не дают ложный Ready.

### 7. Autoscaling, надежность и эксплуатация

- HPA, requests для выбранных utilization metrics, прекращение владения replicas.
- HPA enabled → disabled: восстановление spec.web.replicas и удаление HPA.
- PDB/topology, readiness, drain, конфликты SSA и RBAC.
- Prometheus metrics, dashboard/alerts примеры, Events без секретов.
- Security review, NetworkPolicy overlays, recovery runbooks.

Готовность: масштабирование не вызывает reconcile fight; проверены outage БД/Redis,
rotation Secrets, restart operator, rollout errors, node drain и сохранность uploads.
Ready=true только для наблюдаемой текущей generation и обязательных компонентов.

### 8. Packaging и первый release

- Kustomize install, Helm chart, examples: минимальный HTTP, HTTPS portal, multi-site,
  PostgreSQL, HPA, Push и private images.
- Документация установки, image build, upgrade, rollback, backup/restore boundaries.
- CI release: multi-arch manager image, SBOM, provenance/signing, vulnerability scan,
  минимальные permissions и pinned Actions.
- Dependabot/Renovate и периодический compatibility review через отдельные PR.
- kind E2E с fixture и отдельный Bitrix smoke в доступном лицензированном окружении.

Готовность: установка из release artifact в чистый cluster; опубликованная
compatibility matrix, воспроизводимый build, проверенный upgrade с предыдущего
тестового релиза. Отсутствие настоящего Bitrix smoke отмечается явным ограничением,
а не заменяется успешным HTTP fixture.

## 6. Обязательные cross-cutting tests

1. No-op reconcile; восстановление удаленного ресурса; конфликт владельца.
2. Разные namespace/UID, одинаковые имена объектов, длинные допустимые CR names.
3. Secret missing/key missing/rotation, отсутствие leakage.
4. Autoscaling optional/disabled/enabled, metrics без resource requests.
5. Cron absent agents при disabled; invalid/zero/negative interval при enabled.
6. Optional push/gateway/TLS, удаление отключенных компонентов.
7. PVC retention, shrink rejection, immutable fields, access-mode combinations.
8. Host collisions, nginx/PHP injection, несовпадение site IDs.
9. Route Accepted/ResolvedRefs и stale generation.
10. Manager restart и конкурентные reconciliation attempts.
11. Ready/phase/observedGeneration и отсутствие status update loops.
12. Upgrade release и явные ограничения DB migration/rollback.

## 7. Решения, которые проверяем до реализации соответствующего этапа

Это технические проверки, а не новые требования к CRD:
- API group bitrix.m8.team и совместимость схемы с controller-gen.
- Точные Bitrix settings keys/classes и поддерживаемые версии MySQL/PostgreSQL.
- Контракт runtime images, UID/GID, PHP extensions, non-root entrypoints.
- Куда Bitrix пишет кроме uploads/temp и как это согласуется с immutable code.
- Session mode/TTL=0 semantics и shared Redis memory policy.
- Блокировка cron при отказах MySQL/PostgreSQL.
- Site-validation Job result transport, limits, retry/refresh policy.
- Gateway certificate ownership и фактически выбранный Gateway implementation.

До соответствующей проверки не выдавать неподтвержденное поведение за поддержку.
