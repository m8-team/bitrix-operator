# Безопасность и сетевые профили

Итерация 7.4.1: review границ доверия, opt-in NetworkPolicy profiles и регрессионные
проверки. BitrixCluster API/CRD и runtime templates не меняются. Профили требуют
адаптации к инфраструктуре; CNI enforcement ещё не подтверждён.

## Результаты review

| Область | Проверенное поведение и границы |
|---|---|
| Автор BitrixCluster | Доверенный администратор приложения: выбирает исполняемый image и ссылки на Secrets своего namespace. Право создавать CR фактически позволяет запускать код с этими credentials |
| Manager RBAC | Без wildcard, bind/escalate/impersonate, exec/attach/portforward, serviceaccounts/token и изменения Secrets. Создание Deployments/Jobs всё равно даёт косвенный доступ к ресурсам namespace |
| Secrets | Metadata-only watch, значения читаются через APIReader. Но cluster-wide get/list/watch RBAC остаётся широким: metadata cache не ограничивает права. Manager — привилегированный control plane |
| Workloads | Web, cron, validation и оба Push: non-root UID 979, RuntimeDefault seccomp, drop ALL, запрет privilege escalation, read-only root filesystem, без host namespaces/hostPath/hostPort и автоматического/явного API token |
| Credentials | nginx и release-init не получают PHP credentials. Push получает Redis и signing key, без БД/license/crypto/uploads. PHP исполняет доверенный код со своими credentials |
| Данные | Uploads/PVC сохраняются. Secret revisions используют UID/resourceVersion. ConfigMaps, Events и metrics не предназначены для Secret values; логи пользовательского PHP контролирует приложение |
| Метрики | HTTPS + TokenReview/SubjectAccessReview, metrics-reader только GET /metrics. Сетевая политика не заменяет authentication/authorization |
| Images | Runtime pins и non-root fixture проверены; подписи пользовательских images, SBOM/сканирование и release provenance остаются этапом 8 |

Не выдавайте недоверенным пользователям одного namespace права менять BitrixCluster,
workload images, Pod/namespace labels, NetworkPolicy или RBAC. UID labels защищают от
случайного смешения ресурсов, но не от пользователя с правом их подделки. Разные
trust domains требуют отдельных namespaces и admission/RBAC политики платформы.
Текущий cluster-scoped manager не обеспечивает жёсткую изоляцию недоверенных tenants.
Secret encryption at rest/KMS и аудит API принадлежат кластеру. Review не заменяет
проверку безопасности пользовательского Bitrix, образов и инфраструктуры.

## Профиль приложения

`config/security/workloads` создаёт **выделенный namespace `bitrix-sites`** с Pod
Security Admission restricted (pinned v1.36) и семь NetworkPolicy. Default-deny
действует на **все Pods namespace**, включая будущие и не управляемые оператором.
Его можно установить до создания CR, не ожидая UID. Helper/backup/debug Pods требуют
отдельных правил. Профиль не устанавливает operator, CR, БД или Redis.

| Источник | Назначение | Разрешено |
|---|---|---|
| Gateway data plane | web nginx | TCP 8080 |
| Web / cron / validation | DB Pods | TCP 3306; PostgreSQL overlay заменяет на 5432 |
| Web / cron / validation / Push | Redis Pods | TCP 6379 |
| Pods с owner-uid | CoreDNS | UDP и TCP 53 |
| Web | собственные Push publisher / subscriber | TCP 9010 / 8010 |
| Cron | собственный Push publisher | TCP 9010 |
| Остальные соединения | — | Не разрешены профилем |

Ingress к Push по-прежнему задают operator-owned policies с проверкой UID и роли
источника. Профиль добавляет только egress к Push. Удаление operator-owned policies
закроет Push; не заменяйте их namespace-wide allow. Gateway обращается к nginx,
который проксирует Push. PHP-FPM использует loopback внутри web Pod.

Адаптируйте в своём GitOps overlay:

- Namespace приложения: `bitrix-sites` через Kustomization namespace.
- DB/Redis namespace: `bitrix-data`, Pod labels `app.kubernetes.io/name` со значениями
  `bitrix-database` и `bitrix-redis`. Для нескольких DB connections перечислите peers/ports.
- Gateway **data plane**, не controller: `gateway-system`, Pod label
  `bitrix.m8.team/traffic-role: gateway`. Замените на реальные защищённые labels.
- DNS: `kube-system` + `k8s-app: kube-dns`. NodeLocal DNSCache, hostNetwork и другой
  resolver требуют проверки адресов и собственной политики.

Namespace и Pod selector находятся **в одном peer (AND)**. Два отдельных элемента
дадут OR и расширят доступ. Выбираются backend Pods, а не Service labels/DNS names.

```sh
make kustomize
# Выберите один профиль и адаптируйте его через свой overlay.
bin/kustomize build config/security/workloads > /tmp/bitrix-network.yaml
# Альтернатива для PostgreSQL:
bin/kustomize build config/security/postgresql > /tmp/bitrix-network-postgresql.yaml
```

Проверьте готовый overlay через `kubectl apply --dry-run=server -k <overlay>` в
выбранном тестовом кластере. Применяйте до запуска приложения. Admission проверяет
API/PSS, но не доказывает фильтрацию трафика. Для работающего namespace сначала
инвентаризируйте зависимости: внешний HTTP, SMTP, webhooks, интеграции и лицензирование
по умолчанию закрыты. Default-deny может прервать сайт.

Для DB/Redis вне Kubernetes замените соответствующий `to` на точный `ipBlock` и
TCP port. Пример JSON patch для `bitrix-database` (документационный адрес заменить):

```yaml
- op: replace
  path: /spec/egress/0/to
  value:
    - ipBlock:
        cidr: 192.0.2.10/32
```

Стандартная NetworkPolicy не фильтрует FQDN. DNS failover требует поддерживаемой
платформой FQDN policy/egress proxy либо обновления CIDR. Не открывайте весь Интернет
ради динамического адреса. Service DNAT/SNAT, IPv6, NodeLocal DNS, node-origin traffic
и hostNetwork проверяются на выбранном CNI. NetworkPolicy не шифрует DB/Redis:
TLS/mesh и настройка клиентов — отдельный контракт. Image pulls и CSI mounts
выполняются узлом, а не исходящим соединением PHP Pod.

## Профиль manager

`config/security/manager` содержит только ingress policy в `bitrix-operator-system`.
Включите его рядом с `config/default` или `config/observability` в своём overlay.
Разрешён TCP 8443 от namespace `monitoring` AND Pod label
`app.kubernetes.io/name: prometheus`. Проверьте реальные labels/namespace. Профиль не
меняет сертификаты, SA и metrics-reader binding. **Manager egress остаётся открытым**:
API endpoints, DNS и Service NAT должна настроить инфраструктура. Workload default-deny
к manager неприменим. Kubelet probes с узла размещения не требуют открытия 8081 всем
Pods. NetworkPolicy не защищает от node/admin доступа, включая port-forward.

## Проверки и внедрение

`make test` собирает Kustomize profiles и проверяет разрешённые/запрещённые потоки:
AND namespace/Pod, DB port, прямой Push ingress, чужой UID, Internet egress, оба
направления и объединение с operator-owned Push policies. Проверяются все типы
generated Pods, init containers и чувствительные manager RBAC permissions.
Это модель selectors/ports по отрендеренным правилам, **не сетевой тест CNI**.
Существующий kind E2E запускает runtime Pods с PSA restricted, но не доказывает
enforcement новых policies. Проверки API/CRD и native templates сохраняются.

Следующий gate **7.4.2**: отдельный кластер с enforcing CNI; реальные Service flows
Gateway → web, DB, Redis, DNS TCP/UDP и Push; отрицательные пробы с неверными
namespace/labels/UID; повтор после rollout и пересоздания Pod на другом узле.
Проверять новые соединения после convergence: старые соединения и задержки применения
зависят от CNI. Ready само по себе не доказывает сетевую изоляцию.

Политики аддитивны: чужой allow-all может открыть доступ. Инвентаризируйте все
политики namespace и платформы. Профилями владеет GitOps, оператор их не меняет.
Откат — к предыдущей проверенной версии overlay; удаление default-deny может открыть
трафик и не является безопасным универсальным rollback.

**7.4.3**: recovery для выбранных CSI/DB после определения технологий и RPO/RTO.
См. [эксплуатацию](operations.md), [Push](push-pull.md), [метрики](observability.md).

Источники review (2026-10-07):

- [Kubernetes Network Policies](https://kubernetes.io/docs/concepts/services-networking/network-policies/).
- [RBAC Good Practices](https://kubernetes.io/docs/concepts/security/rbac-good-practices/).
- [Pod Security Standards](https://kubernetes.io/docs/concepts/security/pod-security-standards/).
