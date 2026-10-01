# Cron и agents — итерация 4

Оператор использует существующие `runtime.cron.enabled`, `agents.enabled` и
`agents.interval`. API/CRD не изменены. Все PHP/shell-файлы находятся в
`internal/runtime/templates/cron/` и попадают в ConfigMap без генерации исходного кода в Go.

```yaml
runtime:
  cron:
    enabled: true
    agents:
      enabled: true
      interval: 30s
```

Добавьте этот фрагмент в полный CR. Для отдельного cron Pod uploads PVC должен иметь
`ReadWriteMany`. RWO/RWOP отклоняются до создания ресурсов. Смена access modes
существующего PVC требует переноса данных. Файловая система должна поддерживать
`flock` между узлами; сам признак RWX этого не гарантирует.

## Подготовка приложения

Поддерживается документированный Bitrix режим запуска всех агентов на cron:
`main.check_agents=N`, `main.agents_use_crontab=N`. Подготовьте эти опции через
административный интерфейс/миграцию приложения до включения workload. Удалите из
release безусловные определения `BX_CRONTAB_SUPPORT=true`, `BX_CRONTAB=true` и
`NO_AGENT_CHECK=true` для CLI. Оператор не изменяет application DB options.

Основание: [официальная инструкция Bitrix](https://dev.1c-bitrix.ru/learning/course/index.php?COURSE_ID=37&LESSON_ID=29410),
[CAgent::CheckAgents](https://dev.1c-bitrix.ru/api_help/main/reference/cagent/checkagents.php).
Адаптер самостоятельно написан для этого контракта: задаёт document root и cron
bootstrap constants, подключает `prolog_before.php`, проверяет опции и вызывает
`CAgent::CheckAgents()`. Проверка опций выполняется после bootstrap; неподготовленное
ядро может запустить агенты уже в bootstrap. Поэтому предварительная настройка обязательна.
Sender campaigns, backup и отдельная очередь email не запускаются этим адаптером.

При `cron.enabled=true` PHP-FPM через admin `auto_prepend_file` определяет
`NO_AGENT_CHECK=true` до bootstrap приложения, включая режим `agents.enabled=false`.
В последнем случае cron остаётся здоровым idle workload и не вызывает ядро.
При `cron.enabled=false` оператор сначала удаляет cron Deployment foreground,
дожидается удаления его Pods и только затем обновляет web-конфигурацию. После
отключения действуют настройки приложения; автоматического восстановления DB
опции `check_agents=Y` нет. Другие внешние cron/CLI runners нужно отключить отдельно.

## Исполнение и остановка

- Один Deployment `<cluster>-cron`, одна реплика, `Recreate`. Selector отличается
  от web; Service не направляет HTTP в cron. Образ и resources берутся из `runtime.web.php`.
- Общие release init, projected Secrets, PHP settings, readonly code, UID/GID 979,
  seccomp, отсутствие service account token; uploads/cache доступны на запись.
- PHP требует `proc_open`; `pcntl` не нужен. Shell-wrapper — PID 1, принимает сигналы
  и передаёт запрос остановки PHP supervisor через файл. Supervisor запускает свежий PHP-процесс
  на каждый цикл. Интервал 1s–24h, допустимы дробные секунды, например `1.2s`.
  Это задержка **после завершения** предыдущего цикла; пропущенные запуски не накапливаются.
- Worker удерживает неблокирующий `flock` на
  `<uploads>/.bitrix-operator-agents.lock` до выхода процесса, включая shutdown handlers.
  Файл нельзя удалять/заменять во время работы. Конкурирующий worker пропускает цикл.
- SIGTERM запрещает следующие циклы и ждёт активного worker. Pod имеет grace period
  30 секунд; после него Kubernetes может оборвать выполнение. Долгие агенты должны
  быть идемпотентными и корректно переживать прерывание.
- Heartbeat supervisor отделён от исполнения агента: длинный агент не вызывает
  liveness restart. Readiness включается после успешного цикла (либо сразу в idle),
  снимается при ошибке/занятом lock. Между циклами она отражает последний результат,
  а не непрерывную доступность БД. Логи приложения подавлены, ошибки runner обобщены.
- `CronReady` и `status.runtime.cron` отражают наблюдаемую generation Deployment.
  Общий `Ready` остаётся false до отдельной реализации проверки site IDs ядром.

## Гарантии и проверки

Блокировка защищает от конкуренции workers на исправной общей файловой системе,
в том числе при ручном увеличении replicas. Потеря распределённого lock при
partition/failover storage не имеет fencing-защиты. Exactly-once не гарантируется:
прерывание после внешнего эффекта агента может привести к повторному эффекту.
Несколько независимых операторов/порталов с одной БД и разными uploads не разделяют
этот lock. Работа агентов не зависит от leader election manager.

Envtest проверяет singleton drift, повторную инициализацию reconciler, interval,
release/agent toggle, текущую generation, collision и удаление при отсутствующем
Secret. Native container tests проверяют интервал, конкуренцию двух schedulers,
ошибку worker, отключение, graceful stop и restart на закреплённом PHP image.
Kind E2E проверяет mounts, FPM agent mode, выполнение, отключение агента и удаление
workload. Его статический RWX volume работает на одном узле: это не тест NFS/CSI
при сетевом разделении. Fixture содержит синтетические CAgent/COption, а не
лицензионное ядро Bitrix; production release требует отдельной проверки ядра и модулей.
