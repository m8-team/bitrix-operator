# Web workloads и uploads — итерация 3

Оператор создаёт web Deployment (release init + nginx + PHP-FPM), ClusterIP Service,
uploads PVC и две immutable ConfigMap для каждой версии конфигурации. API
`BitrixCluster` сохранён побайтово. БД и Redis предоставляет инфраструктура.

## Запуск

1. Подготовить release image по [runtime contract](runtime-contract.md), включая
   `/opt/www/bitrix/.settings.release.php` и wrapper. Образ должен содержать `/bin/sh`,
   `cp`, `mkdir`; код должен читаться UID/GID 979. Установку и обновления ядра выполняют
   при сборке release, не через административную панель работающего Pod.
2. Создать Secrets из ссылок CR; значения ключей должны быть непустыми, не более 1 MiB.
   Пароли сохраняются побайтово, включая пробелы и переводы строк.
3. Указать доступные endpoints MySQL/PostgreSQL и Redis с паролем, существующий
   StorageClass, подготовленный release image и hosts. Пример CR содержит placeholders
   для этих значений. Для production использовать digest release.
4. Установить CRD/manager и применить CR. Смотреть `WebReady`, `StorageReady`,
   `runtime.web` и события Kubernetes. Service `<cluster>-web` принимает HTTP на 80.

`WebReady=True` означает, что текущая generation Deployment полностью развёрнута,
все желаемые replicas Ready/Available и PVC Bound. Readiness выполняет PHP settings
wrapper, чтение Secrets, SELECT 1 для каждого управляемого DB connection и Redis
AUTH/PING. Скрипт `ready.php` отвечает только `ready`/`unavailable`; параметры и
ошибки подключений в ответ и PHP log не выводятся. Соединения имеют короткие таймауты.
Probe использует отдельный порт 8081 Pod; Service публикует только 8080.

Liveness проверяет nginx и локальный listener PHP-FPM, не внешние зависимости.
Недоступность БД/Redis исключает Pod из Service, не вызывает цикл рестартов.
Проверка FPM listener не гарантирует отсутствие зависших workers; полноценная проверка
запроса происходит в readiness через FastCGI.

Начиная с итерации 6.1 общий `Ready=True` требует также проверки site IDs через
read-only D7 validation Job. `SitesValidated` и `sites[].exists` отражают результат
для текущего rollout; подробности и ограничения — [Sites](sites.md).

## Storage и жизненный цикл

- Uploads доступны PHP на запись, nginx только на чтение. Код read-only. `EmptyDir`
  из CR используется для temporary и каталогов `bitrix/cache`, `managed_cache`,
  `stack_cache`, `html_pages`; nginx получает только html_pages read-only.
- Pods работают как UID/GID 979, fsGroup 979, non-root, без capabilities и ServiceAccount
  token, с seccomp RuntimeDefault и read-only root filesystem. Storage driver должен
  поддерживать соответствующие права/fsGroup.
- RWO/RWOP: одна replica, стратегия Recreate (с простоем при обновлении). Несколько
  replicas требуют RWX; для него применяется RollingUpdate (surge 1, unavailable 0).
  ReadOnlyMany отклоняется, поскольку uploads должны быть writable.
- PVC не имеет ownerReference: удаление CR сохраняет PVC и данные. Ручное удаление PVC
  по-прежнему зависит от reclaimPolicy PV/StorageClass. Это не заменяет backup.
- Совпадение имени с чужим PVC, другой cluster UID или ownerReferences приводит к
  `StorageCollision`. Повторное создание CR не присваивает старые uploads. Восстановление
  требует отдельной процедуры оператора инфраструктуры: проверить backup, права и
  принадлежность данных, затем явно выполнить миграцию/переименование ресурса.
- StorageClass, access modes и volume mode неизменяемы. Shrink запрещён; увеличение
  request возможно только при `allowVolumeExpansion=true`. Поддержку самого resize
  обеспечивает storage driver; `StorageReady` сообщает Bound, не окончание filesystem resize.
- Старые immutable ConfigMap сохраняются до удаления CR, чтобы предыдущие ReplicaSet
  не потеряли конфиг. Очистка неиспользуемых ревизий — отдельная последующая задача.

## Apply, события и Secrets

Typed apply configurations содержат только поля оператора. Перед каждым SSA проверяются
ownerReference и resourceVersion. Отсутствующие ресурсы сначала создаются обычным create,
чтобы гонка создания не привела к присвоению чужого объекта. Затем собственная запись
Create/Update переносится в Apply официальным helper client-go с проверкой resourceVersion.
Это позволяет удалять из Deployment поля, убранные из CR, и исправляет ранее созданные
ресурсы без пересоздания. Чужие field managers не мигрируются. На принадлежащем CR ресурсе
оператор принудительно восстанавливает управляемые поля; сторонние дополнительные поля
не включаются в apply. Не редактировать replicas/image/config этих Deployment вручную:
источник желаемого состояния — CR. PVC изменяется отдельным optimistic patch.

Индексы зависимостей и watches реагируют на Secrets, StorageClass, retained PVC и owned
Deployment/Service/ConfigMap. Secret informer получает только metadata; Secret data
читается uncached API reader для проверки ключей. Rollout revision — hash имён, UID
и resourceVersion Secrets, без хеширования паролей. Изменение metadata Secret тоже
перезапускает Pod. Credentials монтируются только PHP; init container получает config
без значений Secrets. ImagePullSecrets проверяются на существование, корректность
registry credentials и доступность образов проверяются kubelet.

## Проверки и границы

`make test` проверяет реальный API server (envtest), SSA/idempotence, rollout counters,
rotation, metadata watch, collision, immutable configs, PVC resize, удаление полей
и миграцию ранее созданных записей владения. Unit tests
проверяют pod isolation и допустимые имена ресурсов. `Runtime contract` CI проверяет
синтаксис родными nginx/PHP tools и сохранение settings.

```sh
# Требуются Docker, kind v0.33.0, kubectl, Go 1.26.8, Python 3.
bash hack/e2e-web.sh
```

Скрипт создаёт собственный kind cluster и отдельный kubeconfig, отказывается от
существующего одноимённого кластера и удаляет созданный кластер после теста.
Workflow `Web workloads` проверяет MySQL, PostgreSQL, Redis, HTTP, права mounts,
ротацию crypto Secret, отказ зависимостей и сохранение PVC. Зависимости синтетические;
их создаёт только тест. Проверка реального лицензированного Bitrix требует отдельного
предоставленного release image и не подменяется fixture.

HPA пока возвращает `UnsupportedFeatures`, если включён в CR. Cron/agents, Push и
[Gateway/TLS](gateway-tls.md) реализованы в последующих итерациях. Автоустановка Bitrix, DB migration, backup, NetworkPolicy, TLS к внешним
DB/Redis и production smoke не входят в эту итерацию. Проверено на Linux amd64;
arm64 и конкретные CSI drivers требуют собственных прогонов.
