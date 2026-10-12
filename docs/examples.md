# Примеры установки BitrixCluster

Готовые Kustomize overlays находятся в `config/examples/install/`. Они наследуют
единственный [исходный sample](../config/samples/bitrix_v1alpha1_bitrixcluster.yaml).
`base/` предназначен для наследования. Каждый профиль создаёт один полный CR
`bitrix/example`; это альтернативы для одной установки, не семь разных кластеров.
Выберите один профиль и внесите свои значения в отдельном deployment-репозитории.

| Профиль | Изменение относительно base | Что подготовить |
|---|---|---|
| `http` | 1 web replica, RWO uploads, внутренний HTTP | StorageClass `standard` или ваш аналог; single-replica downtime допустим |
| `https` | TLS сайта, Gateway `edge/shared`, listener `https` | Gateway API v1, Gateway controller, TLS Secret, ReferenceGrant, DNS |
| `multi-site` | s1 → portal, s2 → shop/www.shop | Реальные s1/s2 в БД и release; RWX uploads |
| `postgresql` | PostgreSQL endpoint и порт 5432 | Поддерживающие PostgreSQL ядро Bitrix, PHP image и подготовленная БД; RWX |
| `autoscaling` | 2–6 web replicas, CPU target 70%, requests | Metrics API/metrics-server, RWX uploads, ёмкость worker nodes |
| `push` | Publisher + два subscriber | Push Secret, отдельный Redis endpoint, NetworkPolicy-enforcing CNI и baseline policy |
| `private-image` | Registry с портом, digest и imagePullSecrets | Реальный digest подготовленного Bitrix image и registry Secret; RWX |

В `base` по CRD defaults будет 3 web replicas; `shared-rwx` — имя-пример.
StorageClass должен действительно предоставлять выбранный access mode.
`http` — вариант для одной web replica, без cron и HA; RWO не означает доступ
тома с любого узла. Изменение storage/accessModes существующего PVC не является
поддерживаемым способом переключения профилей — см. [storage lifecycle](web-workloads.md).

## Перед применением

Установите оператор по [инструкции](installation.md). Создайте namespace `bitrix`
и подготовьте внешние DB/Redis, uploads StorageClass и собственный immutable
Bitrix release image по [runtime contract](runtime-contract.md). В образ должны
войти реальный Bitrix и штатные operator overlays; приведённый
`registry.example.com/bitrix/portal` не существует как готовый дистрибутив Bitrix.

Замените image, hosts/site IDs, endpoints, database/username, StorageClass,
размеры томов и имена Secrets. В private-image digest из 64 символов `a` —
**заглушка**, а не опубликованный образ. Указанный tag сохраняется в API,
но при наличии digest оператор использует digest для pull.

Все Secrets заранее создаются в namespace CR. Значения не хранятся в примерах
и не генерируются оператором. Используйте свой Secret manager или файлы локально,
не передавайте реальные значения через аргументы shell/логи CI.

| Secret по умолчанию | Key / тип | Когда нужен |
|---|---|---|
| `bitrix-license` | `license`, Opaque | Всегда: действительная лицензия для вашего ядра |
| `bitrix-crypto` | `key`, Opaque | Всегда: существующий ключ Bitrix Crypto; сохранять при recovery |
| `bitrix-database` | `password`, Opaque | Всегда: пароль подготовленного DB user |
| `bitrix-redis` | `password`, Opaque | Всегда: пароль Redis |
| `portal-tls` | `tls.crt` + `tls.key`, kubernetes.io/tls | HTTPS: сертификат покрывает все hosts сайта |
| `bitrix-push` | `key`, Opaque | Push: требования к ключу в [Push guide](push-pull.md) |
| `bitrix-registry` | `.dockerconfigjson`, kubernetes.io/dockerconfigjson | Private registry: credentials с pull-доступом |

`release.imagePullSecrets` относятся к workload images приложения; это отдельная
настройка от credentials для скачивания самого manager image из GHCR.

## Render, проверка и установка

Команды ниже предполагают, что значения уже заменены и namespace/Secrets готовы.
Не применяйте весь каталог `config/examples/install` рекурсивно.

```bash
# Проверить полный итоговый CR перед отправкой в кластер.
./bin/kustomize build config/examples/install/http > /tmp/bitrix-install.yaml
kubectl apply --dry-run=server --validate=strict -f /tmp/bitrix-install.yaml
kubectl apply -f /tmp/bitrix-install.yaml
kubectl -n bitrix wait bitrixcluster/example --for=condition=Ready --timeout=600s
kubectl -n bitrix get bitrixcluster/example -o yaml
```

Dry-run проверяет schema/CEL и admission; он не проверяет лицензию, Secret values,
наличие site IDs, сетевую доступность или содержимое image. Их проверяет reconcile
и дальнейшая эксплуатационная проверка. `Ready` требует актуальных статусов
включённых компонентов и site validation, а не только запущенного nginx.

Для HTTPS сначала подготовьте инфраструктурные ресурсы из
[gateway-https.yaml](../config/examples/gateway-https.yaml): namespaces `bitrix`
и `edge`, GatewayClass `shared`, сертификат и Gateway/ReferenceGrant. Этот файл
не устанавливает Gateway controller. Затем применяйте профиль `https`.
Подробности certificate ownership и rollout — [Gateway guide](gateway-tls.md).

Для Push **до включения CR** установите независимую baseline policy:

```bash
kubectl -n bitrix apply -f config/examples/push-default-deny.yaml
kubectl apply -k config/examples/install/push
```

Политика намеренно не включена в один apply с CR: сначала должна действовать
изоляция. Не удаляйте её, пока существуют Push Pods. Redis для Push должен быть
отдельным от других BitrixCluster: `cache.sid` не изолирует глобальные Push keys.

Примеры multi-site и PostgreSQL не меняют ядро/редакцию Bitrix, не создают сайты
в БД и не мигрируют данные между СУБД. Возможность работы вашей версии Bitrix с
PostgreSQL проверяется отдельно. Multi-site без Gateway доступен через внутренний
HTTP Service; для внешнего HTTPS объедините sites/TLS с подходящим listener,
который покрывает все hosts, как описано в [sites](sites.md) и Gateway guide.

## Проверки в репозитории

`make test-examples` рендерит все семь профилей стандартным Kustomize с включёнными
load restrictions. `hack/e2e-chart.sh` создаёт новый kind cluster, устанавливает
operator/CRD, проверяет каждый профиль через server-side dry-run и убеждается,
что ни один CR не был сохранён. Реальные image/Secret/DB зависимости в этом
schema-тесте не требуются. Runtime поведение покрывается отдельными fixture E2E.
