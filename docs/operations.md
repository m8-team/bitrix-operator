# Диагностика Bitrix Operator

Команды читают состояние; замените `tenant` и `portal` на namespace и имя CR.
Не публикуйте Secret values, полный env контейнеров или credentials из конфигов.

```sh
kubectl -n tenant get bitrixcluster portal -o jsonpath='{.metadata.generation}{" / "}{.status.observedGeneration}{"\n"}{.status.conditions}'
kubectl -n tenant get pods,pvc,hpa,pdb,httproutes
kubectl -n tenant get events.events.k8s.io --sort-by=.metadata.creationTimestamp
kubectl -n bitrix-operator-system logs deployment/bitrix-operator-controller-manager -c manager --since=10m
```

## Cluster not ready

Для `BitrixClusterNotReady` найдите False/Unknown conditions **текущей generation**.
Устаревшая или отсутствующая condition экспортируется как Unknown и не означает
доказанный отказ компонента.

| Condition / ситуация | Проверка и следующий шаг |
|---|---|
| StorageReady | PVC Bound, StorageClass, события CSI, size/access modes. Несколько Pod/cron требуют RWX. Не удаляйте PVC для «исправления» |
| WebReady | Deployment rollout, scheduling, image pull, init release/probes, доступ к внешним DB/Redis |
| SecretMissing / SecretKeyInvalid | Наличие Secret/названий keys, namespace и права; проверяйте метаданные, не выводите значения |
| CronReady | Singleton rollout, scheduler logs, agents и filesystem lock; см. cron-agents.md |
| PushReady | Publisher/subscriber, Secret revision, Redis и CNI/NetworkPolicy; см. push-pull.md |
| SitesValidated | Validation Job для текущих image IDs/generation, site IDs в ядре Bitrix; см. sites.md |
| GatewayReady | Accepted/ResolvedRefs, listener, hosts, TLS certificate и ReferenceGrant; см. gateway-tls.md |
| AutoscalingReady | Metrics API, HPA conditions, requests обоих web containers, min/max; см. autoscaling.md |

Readiness зависит от DB/Redis, liveness — от локального процесса. Массовый restart
web не восстанавливает внешнюю БД: сначала восстановите зависимость, затем ждите
Ready для текущей generation.

## Reconcile stalled

Для `BitrixClusterReconcileStalled` / `BitrixOperatorReconcileErrors` проверьте
manager Pods, Lease leader election, API connectivity, RBAC и конфликты в логах.
Сравните установленные роли с текущими сгенерированными манифестами; не заменяйте
роли на cluster-admin. При обновлении operator image обновляйте RBAC.

`ResourceCollision` означает несовпадение controller owner UID. Сопоставьте
ownerReferences с UID CR и определите владельца/план миграции до ручных изменений.
Для SSA конфликтов проверьте managedFields и конкурирующий источник записи;
не запускайте бесконтрольный force apply на все ресурсы.

Если generation уже наблюдена, но component False, используйте диагностику этого
компонента: это не задержка наблюдения generation.

## Metrics unavailable

Для `BitrixOperatorCollectionFailed` проверьте синхронизацию cache, API и права
list/watch BitrixCluster. Ошибка чтения не подменяется здоровыми нулями.

HTTP 401 означает проблему authentication, 403 — отсутствие GET `/metrics` для
ServiceAccount Prometheus. TLS error требует проверки CA, SAN, срока сертификата
и имени Service. Проверьте selectors ServiceMonitor/PrometheusRule, endpoints и
NetworkPolicy. Используйте [HTTPS-профиль](observability.md), не отключайте проверку
сертификата в рабочем окружении.

## Обслуживание узла

Проверьте здоровые реплики, PDB disruptionsAllowed, RWX и ресурсы другого узла.
Для непрерывного обслуживания нужны минимум две здоровые web-реплики, при HPA —
minReplicas >= 2. Используйте Eviction API / kubectl drain с обычным grace period:
PDB не защищает от прямого delete/force bypass. Завершение web Pod может занять
360 секунд; см. [graceful drain](disruptions.md).

## Данные и восстановление

Uploads PVC сохраняется после удаления CR. Новый CR с тем же именем имеет другой
UID и получает StorageCollision; нужен явный план переноса данных/PVC и проверенный
backup. Не снимайте ownership guards и не очищайте labels PVC вслепую.

Backup должен согласовывать внешнюю БД, uploads, release image и инфраструктурные
Secrets/ключи. Оператор не выполняет backup/restore, DB migrations или rollback схемы.
Откат image не отменяет изменения БД. Проверяйте restore на отдельной установке
до переключения трафика. Ротация crypto key не является универсальным исправлением:
она может сделать ранее зашифрованные данные недоступными.

Security review и opt-in NetworkPolicy profiles описаны в [security.md](security.md).
Реальный enforcement проверен на Cilium в итерации 7.4.2:
[сценарии, результат CI и ограничения](network-policy-tests.md).
Recovery runbooks и restore drill для выбранных CSI/DB, включая RPO/RTO,
остаются итерацией 7.4.3.
