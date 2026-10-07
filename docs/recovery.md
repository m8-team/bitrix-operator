# Backup и восстановление

Итерация **7.4.3a**: runbook и воспроизводимый logical restore drill для MySQL,
PostgreSQL и uploads. CI-сценарий добавлен; подтверждением служит успешный job
`Recovery drill` на конкретном commit. Production CSI, целевые RPO/RTO и проверка
лицензированного Bitrix остаются отдельным gate **7.4.3b**.

Оператор управляет workloads и сохраняет uploads PVC при удалении CR. Он не делает
backup, PITR, DB migrations или автоматическое восстановление. Сохранённый PVC
не является backup: потеря storage/namespace/PVC может уничтожить данные.

## Состав согласованной копии

| Часть | Что сохранить и проверить |
|---|---|
| БД | Все используемые connections, схему/данные, routines/triggers/events или extensions, server/client versions, роли/grants и необходимые внешние зависимости |
| Uploads | Полный filesystem, скрытые файлы, владельцев/права, symlinks и используемые ACL/xattrs; checksum или подтверждённый CSI snapshot/export |
| Release | Доступный immutable image digest, runtime images, версия оператора, исходный CR spec и инфраструктурные manifests |
| Ключи | Точные версии crypto/license/Push keys и credentials из Secret manager; доступ к KMS и процедуре расшифровки backup |
| Инфраструктура | StorageClass/CSI и snapshot class, access modes, capacity, reclaim policy, Gateway/TLS, NetworkPolicy, DB/Redis endpoints |
| Manifest | Backup ID, UTC consistency point, source CR/PVC UID, DB coordinates при PITR, checksums, размеры, status complete, срок хранения |

Secrets и дампы храните зашифрованными, с ограниченным доступом и копией вне исходного
кластера/аккаунта хранения. Base64 Kubernetes Secret не шифрует данные. Checksums
выявляют повреждение относительно доверенного manifest, но не заменяют его подпись
и защиту от изменения. Проверяйте доступность release image и расшифровку backup
в рамках каждого drill. Смена crypto key при восстановлении может лишить доступа
к ранее зашифрованным данным; сначала восстановите исходный ключ.

Redis cache можно прогреть заново; потеря sessions означает повторный вход пользователей.
Не переносите этот вывод на произвольные пользовательские очереди/данные в Redis:
их владельцы должны определить собственную persistence/backup стратегию.

## Получение consistency point

1. Зафиксировать backup ID и остановить все источники записи: входящий трафик,
   фоновые agents/cron, HTTP agents, внешние интеграции и DB jobs. Drain активных
   запросов/транзакций должен завершиться. Одного отключения Gateway недостаточно.
2. Обеспечить сохранение остановки до конца копирования. В текущем CRD нет suspend:
   ручной scale управляемого Deployment будет отменён reconcile/HPA. Используйте
   проверенный application maintenance/fencing; при остановке manager учитывайте,
   что это затронет все обслуживаемые им CR. Удаление Secret не останавливает уже
   запущенные Pods и не является способом quiesce.
3. Снять DB backup и uploads из одного интервала без записи. Для нескольких БД
   нужна общая прикладная граница, а не несколько независимых online dumps.
   CSI snapshot сам по себе не согласует filesystem с внешней БД.
4. Проверить коды завершения, checksums, наличие всех частей и возможность чтения.
   Пометить backup complete только после проверки; снять fencing и записать время.

MySQL InnoDB: `mysqldump --single-transaction --quick` даёт согласованный снимок
транзакционных таблиц; concurrent DDL и нетранзакционные таблицы требуют отдельного
плана. Явно включайте нужные routines/events/triggers. PostgreSQL `pg_dump -Fc`
копирует одну БД; роли и tablespaces не входят в эту копию. Используйте совместимые
client/server versions и отдельно восстанавливайте необходимые globals/extensions.
Для больших БД, малого RPO и online backup нужны штатные механизмы выбранного DB
оператора/provider: PITR/binlog/WAL вместе с согласованной точкой uploads.

## Восстановление в отдельную установку

1. Объявить incident time, выбрать последнюю проверенную согласованную копию.
   Оставить исходную установку fenced. Не удалять её PVC, PV или backup.
2. Создать отдельный namespace, новую БД и Redis, подготовить доступный release
   digest и storage. Отключить Gateway, cron, Push и внешние интеграции. На время
   восстановления использовать одну web-реплику без HPA, если storage допускает её.
3. Создать новый CR с нужным spec, но **не предоставлять один обязательный Secret**.
   Это gate только для новой установки без существующих workloads: дождаться
   `Ready=False / SecretMissing` и проверить отсутствие Deployments/Jobs/Pods
   приложения. Если объекты уже существуют, остановиться: этот gate их не останавливает.
4. Подготовить **новый** PVC для нового CR UID. Имя должно совпадать с
   `workload.Name(cr, "uploads")` (длинные/составные имена нормализуются с hash).
   Нужны label `bitrix.m8.team/owner-uid=<новый UID>` и annotation
   `bitrix.m8.team/cluster-name=<имя CR>`, без ownerReferences; storage spec должен
   совпадать с CR. Создавать через `create`, не менять ownership существующего PVC.
   Если нужна CSI snapshot restore, параметры dataSource и binding готовит администратор
   выбранного CSI до предоставления Secret. Проверить reclaim policy и capacity.
5. Восстановить данные в пустую БД и uploads в новый PVC через ограниченный helper
   Pod. PostgreSQL restore должен прекращаться на ошибке; MySQL partial import при
   сбое означает пересоздание пустой целевой БД и повтор, а не продолжение вслепую.
   Проверить количество записей, file hashes, владельцев/права и прикладные связи
   файлов с БД. Не восстанавливать status, resourceVersion, managedFields, старые
   ownerReferences или claimRef UID из выгрузки Kubernetes объектов.
6. Удалить helper, проверить завершение записи и сохранность ключей. Только затем
   предоставить обязательный Secret: оператор должен принять новый PVC с его UID
   reservation и вывести приложение в Ready для текущей generation.
7. Провести smoke настоящего Bitrix: нужные sites, чтение/запись файла, запись в БД,
   расшифровка тестовых данных, авторизация, работа модулей. `Ready` и HTTP 200
   без этих проверок не доказывают восстановление пользовательских данных.
8. После проверки переключить трафик, затем разрешить cron/интеграции/Push и нужный
   масштаб. Записать recovery complete time. Не допускать одновременную запись старой
   и новой установок. После новых записей в целевой БД обратное переключение требует
   согласования данных: простой возврат маршрута может их потерять.

При `StorageCollision` остановиться и проверить CR/PVC UID. Новый CR с прежним
именем специально не присваивает сохранённый PVC. Предпочтительна новая установка
с переносом данных. Этот runbook не разрешает стирать reservation старого PVC.

## Воспроизводимый CI drill

```sh
bash hack/e2e-recovery.sh
```

Нужны Linux, Docker, Go/make, kubectl, kind 0.33.0 и Python 3. Скрипт создаёт новый
kind 1.37.0 с отдельным временным kubeconfig и уникальным именем; не принимает
существующий cluster. Cleanup удаляет только созданный кластер и временные данные.
Fixtures, backup и credentials синтетические; dumps/Secrets не публикуются в artifacts.

Проверяется реальный оператор с MySQL 8.4.11, PostgreSQL 17.11 и Redis 8.2.10
из существующего dependency fixture. Storage — kind `standard`/local-path, **не CSI**.
Вместо лицензированного Bitrix запускается существующий PHP runtime fixture.

- В БД записывается marker с SHA-256 файла uploads. При отсутствии других writers
  снимаются логические дампы, tar каталога fixture `upload/recovery/`, CR spec
  и четыре обязательных Secret.
- Повреждённый tar отвергается до импорта. Локальные tests также проверяют повреждение
  DB dumps/ключей, отсутствие файлов, неполный manifest и symlink.
- После backup создаются второй marker и файл. Затем исходные CR удаляются,
  DB endpoint отключается; retained PVC остаётся, повторный CR получает StorageCollision.
- В новых namespaces поднимаются пустые БД. Новый CR остаётся SecretMissing,
  новый PVC заполняет non-root helper; затем восстанавливаются Secret и стартует runtime.
- Проверяются новые PVC UID, точные Secret values, содержимое и UID/GID файла,
  соответствие file hash записи в БД, отсутствие второго marker/файла, Ready и HTTP.

Сценарий не тестирует шифрование Bitrix, KMS, все виды SQL объектов, DB cluster/PITR,
CSI snapshot/restore, RWX/distributed locking, ACL/xattrs, потерю всего кластера или
production fencing. Он проверяет путь восстановления из копии и границу данных.
Архив содержит только каталог fixture `recovery/`, принадлежащий UID/GID 979,
а не корень mount, управляемый kubelet. Полное сохранение filesystem metadata
требует отдельного storage drill.

## RPO/RTO и оставшийся production gate

`RECOVERY_REPORT` в CI содержит `recovery_point_age_seconds` (incident минус
consistency point), `restore_elapsed_seconds` (от начала имитации аварии до проверок
обоих runtime) и число намеренно невосстановленных markers. Создание исходного kind
и build images не входят в это время. Это измерение маленького fixture, не SLA.

Для **7.4.3b** нужно выбрать CSI/StorageClass и backup backend, topology/access modes,
конкретную DB deployment/backup технологию, размеры данных и цели RPO/RTO.
Drill должен включать потерю исходного хранилища, доступ к off-cluster копии и ключам,
provisioning, download/decrypt, restore DB/uploads, Bitrix smoke и переключение трафика.
Записывайте фактическую последнюю восстановленную транзакцию, потерянный интервал,
длительность каждой фазы и результат относительно согласованных целей.

Источники:

- [MySQL 8.4 mysqldump](https://dev.mysql.com/doc/refman/8.4/en/mysqldump.html).
- [PostgreSQL 17 pg_dump](https://www.postgresql.org/docs/17/app-pgdump.html) и
  [pg_restore](https://www.postgresql.org/docs/17/app-pgrestore.html).
- [Kubernetes Persistent Volumes](https://kubernetes.io/docs/concepts/storage/persistent-volumes/).
