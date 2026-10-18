# Наблюдаемость оператора

Итерация 7.3 добавляет метрики BitrixCluster и Events через `events.k8s.io/v1`.
BitrixCluster API/CRD и конфигурации приложения не меняются. Это наблюдаемость
control plane: HTTP latency, PHP-FPM saturation и состояние DB/Redis требуют
отдельных exporters и проверок пользовательских операций.

## Метрики

Метрики доступны на существующем HTTPS `/metrics` manager вместе со штатными
`controller_runtime_*`, `workqueue_*`, Go/process metrics. Collector читает список
CR из cache один раз на scrape, не обращается к Secrets или внешним сервисам.
Работает на leader и follower; после restart не требует reconcile каждого CR.
Серии не хранятся в GaugeVec: при удалении или начале удаления CR они исчезают
со следующего scrape.

| Метрика | Labels | Значение |
|---|---|---|
| `bitrix_cluster_generation` | `cluster_namespace`, `cluster` | Текущая generation в cache |
| `bitrix_cluster_observed_generation` | те же | Последняя сохранённая observedGeneration |
| `bitrix_cluster_condition` | те же + `condition`, `status` | One-hot 0/1; status = True/False/Unknown |
| `bitrix_cluster_replicas` | те же + `component`, `state` | Последние сохранённые счётчики workload |
| `bitrix_operator_collection_success` | нет | 1 — cache прочитан, 0 — ошибка/таймаут |
| `bitrix_operator_collection_duration_seconds` | нет | Время сбора snapshot |
| `bitrix_operator_reconcile_stage_duration_seconds` | `stage`, `outcome` | Histogram длительности каждой стадии reconciliation |
| `bitrix_operator_reconcile_stage_attempts_total` | `stage`, `outcome` | Число завершений стадий по disposition |

Conditions ограничены восемью типами: Ready, StorageReady, WebReady, CronReady,
PushReady, SitesValidated, GatewayReady, AutoscalingReady. Отсутствующая condition
или несовпадение её observedGeneration / status.observedGeneration с generation
дают Unknown=1, True=False=0. Старый Ready не выдаётся за текущий.
Отключённые компоненты отражают True/Disabled из CR status.

Components: `web`, `cron`, `push_publisher`, `push_subscriber`.
States: `desired`, `ready`, `updated`, `available`. При отсутствии runtime status
счётчиков нет. Существующие счётчики могут быть устаревшими — проверяйте generation.
Для HPA desired берётся из наблюдённого Deployment через status.

Не более 42 серий на CR и две серии snapshot collector на manager.
Дополнительные метрики стадий имеют фиксированный список stage
(`prepare`, `config`, `push`, `web`, `cron`, `web-status`, `sites`,
`config-gc`, `gateway`, `other`) и outcome (`success`, `Waiting`, `Blocked`,
`Transient`). Никакие per-CR labels для этих метрик не создаются. Histogram
помогает обнаружить замедление конкретного этапа, а counter отделяет штатное
ожидание от постоянной блокировки и временных ошибок. Нет labels с UID,
Secret values, image tags/digests, hostnames, site IDs, reason/message или error.
Авторизованные читатели metrics видят имена CR и namespace. `cluster_namespace`
не конфликтует с target label `namespace` ServiceMonitor.

При агрегации нескольких Kubernetes-кластеров добавьте уникальный
`kubernetes_cluster` к сериям через scrape relabeling либо вашу систему агрегации.
Локальные queries Prometheus сами по себе не получают `external_labels`.
Иначе одинаковые namespace/CR разных кластеров будут смешаны.

При ошибке cache read возвращаются success=0 и duration, а серии CR отсутствуют.
Успешный HTTP scrape не означает успешного чтения cache. Таймаут чтения — 2 секунды.
Cache имеет eventual consistency; generation не доказывает доступность сайта
в реальном времени.

## Подключение Prometheus

`config/observability` — отдельный Kustomize профиль с manager, ServiceMonitor,
PrometheusRule и binding существующего metrics-reader ClusterRole. Он не
устанавливает Prometheus Operator, Grafana, cert-manager или PKI.

1. Установите monitoring CRDs. Настройте selectors Prometheus для выбора
   ServiceMonitor и PrometheusRule этого профиля.
2. Подготовьте namespace `bitrix-operator-system` и Secret
   `bitrix-operator-metrics-tls` с `tls.crt`, `tls.key`, `ca.crt`. Сертификат должен
   содержать SAN `bitrix-operator-controller-manager-metrics-service.bitrix-operator-system.svc`.
   Secret и его ротация принадлежат инфраструктуре; приватный ключ не хранится в Git.
3. В `metrics-reader-binding.yaml` замените `monitoring/prometheus` на реальный
   ServiceAccount сервера Prometheus. Он получает только GET `/metrics`.
4. В своём overlay замените manager image `controller:dev` на собранный образ.
   Проверьте результат: `bin/kustomize build config/observability`.

TLS verification включена, используются CA и serverName. Bearer token берётся из
projected ServiceAccount volume самого Prometheus; его обновляет kubelet.
Prometheus должен монтировать token и разрешать этот `bearerTokenFile`. Если политика
Prometheus Operator запрещает filesystem references, используйте поддерживаемый
платформой `authorization.credentials` Secret с автоматической ротацией.
Не отключайте TLS verification или authn/authz для обхода ошибок настройки.
При смене namespace/Service имени обновите SAN и ServiceMonitor serverName.

Базовый `config/default` работает без monitoring CRDs с self-signed metrics
certificate. Старый scaffold `config/prometheus` — отдельный пример; для
проверяемого TLS используйте новый профиль.

## Alerts и dashboard

Четыре правила: NotReady после 10 минут, непрочитанная generation после 15 минут,
collection failure после 5 минут и постоянные reconcile errors после 10 минут.
Пороги — стартовые значения, настройте их под rollout/SLO. CR alerts агрегируют
manager replicas, сохраняя Kubernetes cluster / namespace / CR; ошибки manager
остаются видны по instances. Исчезновение CR не является ошибкой.

Отдельно настройте стандартный `up == 0` для metrics targets и alert на отсутствие
самих targets. Исчезнувший exporter требует внешнего inventory: эти правила не
обнаруживают его по отсутствующим собственным метрикам.

Импортируйте `config/observability/dashboard.json` в Grafana. Выберите Prometheus
datasource, Kubernetes cluster, namespace и CR. Панели показывают Ready, generation
lag, replicas, collection success и reconcile errors. CI проверяет JSON;
отрисовка и подключение к конкретной версии Grafana проверяются на вашей платформе.

`bash hack/test-observability.sh` проверяет правила через promtool 3.15.0:
длительный отказ, recovery, удаление CR, dedup manager replicas, ошибки collector/
reconcile и временные пороги. Linux amd64 binary загружается с проверкой SHA-256;
для другой платформы укажите `PROMTOOL=/path/to/promtool`.
Kind проверяет HTTPS authn/authz и реальные Events. Профиль с инфраструктурным
сертификатом проверяется сборкой Kustomize; реальная PKI/Prometheus интеграция
требует проверки в вашем окружении.

## Events

Используется современный `mgr.GetEventRecorder`, API `events.k8s.io/v1` и RBAC
`create;patch`. После успешной записи status фиксируется переход в
`ClusterProgressing` (Normal), `ClusterDegraded` (Warning) или `ClusterReady` (Normal).
Тексты/reasons фиксированы, подробности компонентов находятся в status.conditions.
Смена причины внутри того же состояния не создаёт Event. No-op reconcile и restart
не повторяют уведомление.

Events — best effort с ограниченным сроком хранения Kubernetes. Сбой между записью
status и отправкой может потерять Event; это не audit log и не exactly-once delivery.
Источник текущего состояния — CR status и его generation.

См. [диагностику и восстановление](operations.md).
Источники: [Prometheus exporters](https://prometheus.io/docs/instrumenting/writing_exporters/),
[Kubebuilder metrics](https://book.kubebuilder.io/reference/metrics),
[controller-runtime recorder](https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/recorder).

## OBS-01 — расширенная диагностика при масштабировании

Дополнительно экспортируются:
- `bitrix_operator_reconcile_outcomes_total{result}`: завершения полного reconcile, только фиксированные result = ready/degraded/progressing/error/other (без имён CR/Secrets и текста ошибок). Счётчик публикуется активным manager.
- `bitrix_operator_collection_clusters` и `bitrix_operator_collection_unready_clusters`: число активных CR и CR без подтверждённого Ready текущей generation **на каждый экземпляр manager**. Для глобального количества используйте `max`, а не `sum` (followers тоже выполняют scrape).
- `bitrix_cluster_site_validation_last_success_timestamp_seconds{cluster_namespace,cluster}`: Unix-время последней **сохранённой успешной** проверки, включая уже устаревший результат. Для возраста используйте `time() - metric`; отсутствие серии означает отсутствие подтверждённой проверки, а не возраст 0. Поля identity, Job UID и сообщения не экспортируются.

Новые dashboard panels: p95 времени reconciliation-стадий, распределение итогов,
число CR в cache, возраст успешной проверки сайтов. Промежуточные метрики
остаются без per-CR labels, что позволяет видеть нагрузку 10/100/1000 CR
без добавления чувствительных high-cardinality лейблов. При ошибке cache read
новые числовые summary-серии отсутствуют — не подменяйте это нулевой нагрузкой.
См. [PERF-01](performance.md) для воспроизводимого нагрузочного профиля.
