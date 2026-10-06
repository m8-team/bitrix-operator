# Web: обслуживание узлов и завершение запросов

Оператор создаёт `policy/v1 PodDisruptionBudget <cluster>-web` с
`maxUnavailable: 1` (целое число), `unhealthyPodEvictionPolicy: AlwaysAllow`
и селектором web Deployment, включающим UID BitrixCluster. Cron, Push и Jobs
в него не входят. Бюджет следует фактическому масштабу Deployment, в том числе
при HPA; отдельное изменение PDB при scale не требуется. Оператор восстанавливает
удалённый PDB и свои поля, сохраняет чужие аннотации и отклоняет чужого владельца.

Для доступности во время обслуживания нужны как минимум две здоровые web-реплики,
RWX uploads, доступные узлы и запас ресурсов для замены. При HPA задайте
`minReplicas >= 2`. Одна реплика допускает eviction и перерыв обслуживания.
`AlwaysAllow` позволяет убрать нездоровый Pod без блокировки node drain.
PDB ограничивает добровольные eviction; он не предотвращает отказ узла,
прямой delete, HPA scale-in или Deployment rollout. Для RWX rollout по-прежнему
используется `maxUnavailable: 0`, `maxSurge: 1`; для RWO — `Recreate`.

Web Pod предпочитают равномерное распределение по `kubernetes.io/hostname`
с `maxSkew: 1`, `ScheduleAnyway`. Это предпочтение, а не обязательная
anti-affinity: при недостатке узлов допускается совместное размещение.
Zone spreading и изменение CRD в этой итерации не добавлены.

## Порядок завершения

1. Нативный nginx `preStop` создаёт `/tmp/draining`: `/ready` отвечает 503,
   `/live` остаётся независимым. Затем ждёт 10 секунд на распространение
   изменения EndpointSlice/Gateway и отправляет nginx master `SIGQUIT`.
2. nginx прекращает приём соединений и завершает активные запросы, в том числе
   FastCGI. `worker_shutdown_timeout 300s` ограничивает ожидание, включая
   долгоживущие WebSocket-соединения. Клиенты Push должны переподключаться.
3. PHP `preStop` ждёт удаления nginx master pidfile через read-only mount
   nginx temporary volume. Master удаляет файл после выхода workers;
   закрытие listening socket само по себе не считается завершением запросов.
   После этого PHP-FPM получает `SIGQUIT`.
4. Оба hooks удерживают выполнение до выхода процесса, чтобы runtime не
   отправил ранний `SIGTERM`. Общий `terminationGracePeriodSeconds` — 360.
   Ожидание PHP ограничено 320 секундами для stale pidfile и ещё 25 секундами
   после QUIT; nginx hook ограничен 10 + 310 секундами.

Конфигурации и shell scripts находятся в `internal/runtime/templates/`,
монтируются из версионированных ConfigMap. PID namespace не разделяется,
оба сервера работают как PID 1 в своих контейнерах. Cron/validation Jobs
не получают web hooks. При рестарте nginx очищает старый readiness marker.

Окно в 10 секунд не гарантирует скорость обновления любого внешнего балансировщика.
Запросы дольше drain timeout, принудительное удаление с нулевым grace period,
падение процесса/узла и потеря storage не покрываются graceful shutdown.
Hooks запускаются также при liveness restart: если PHP перезапускается отдельно
при работающем nginx или nginx оставил stale pidfile, ожидание может занять
320 секунд. Доступность в это время обеспечивают другие здоровые реплики.

## Проверка

`make test` проверяет API/SSA ownership, no-op reconcile, drift repair,
восстановление PDB, конфликт владельца, HPA и изоляцию web hooks от cron.
`bash hack/e2e-web.sh` создаёт новый изолированный двухузловой kind и проверяет
распределение двух реплик, отклонение второго eviction, успешное завершение
активного PHP-запроса после закрытия nginx listeners во время targeted `kubectl drain`, замену Pod на другом
узле и сохранность uploads. Для этого используется синтетический PHP endpoint.
Общий host-каталог двух kind nodes — только test fixture: распределённый CSI,
сетевые разделения и настоящее Bitrix-приложение требуют отдельной проверки.

Справка: [Kubernetes disruptions](https://kubernetes.io/docs/concepts/workloads/pods/disruptions/),
[pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/),
[topology spread](https://kubernetes.io/docs/concepts/scheduling-eviction/topology-spread-constraints/),
[nginx signals](https://nginx.org/en/docs/control.html),
[PHP-FPM signals](https://github.com/php/php-src/blob/PHP-8.2/sapi/fpm/php-fpm.8.in).
