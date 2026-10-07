# Bitrix Operator

Kubernetes operator для коробочного портала и сайтов Bitrix.

**Итерация 7.4.2: NetworkPolicy enforcement проверен на Cilium.** Оператор создаёт nginx/PHP Deployment,
HTTP Service, retained uploads PVC и versioned конфиги из родных файловых шаблонов.
Поддержаны внешние MySQL/PostgreSQL и Redis, Secret rotation и статусы rollout.
[Запуск и ограничения](docs/web-workloads.md). `WebReady` отражает работу web;
общий `Ready=False` до проверки site IDs через ядро Bitrix.

## API и разработка

- API: `bitrix.m8.team/v1alpha1`, namespaced `BitrixCluster`.
- [План итераций](docs/implementation-plan.md).
- [Backup/restore runbook и изолированный recovery drill](docs/recovery.md):
  MySQL/PostgreSQL + uploads; production CSI/RPO/RTO требуют отдельной проверки.
- [Границы доверия и opt-in NetworkPolicy](docs/security.md): MySQL/PostgreSQL/manager;
  [Cilium enforcement E2E](docs/network-policy-tests.md) выполняется отдельным workflow.
- [Foundation: версии, проверки и ограничения](docs/foundation.md).
- [Архитектурное решение](docs/adr/0001-api-foundation.md).
- [Исходный контракт](docs/reference/bitrixcluster_types.go.txt) сохранён побайтово
  в [api/v1alpha1/bitrixcluster_types.go](api/v1alpha1/bitrixcluster_types.go).
- [Пример HTTP без TLS](config/samples/bitrix_v1alpha1_bitrixcluster.yaml).

Нужны Go **1.26.8**, make, git и доступ к Go modules/GitHub releases.
`make test` запускает локальные etcd и kube-apiserver через envtest, Docker не нужен.

```sh
make build
make test
make lint-config lint
make verify-generated
```

Дополнительная проверка предыдущего Kubernetes minor:

```sh
make test ENVTEST_K8S_VERSION=1.36.0
```

Установка закреплённого Kubebuilder CLI для дальнейшего scaffold (Linux amd64/arm64,
с проверкой SHA-256):

```sh
bash hack/install-kubebuilder.sh
export PATH="$PWD/bin:$PATH"
kubebuilder version
```

## Локальная проверка с отдельным Kubernetes-кластером

Следующие команды используют **текущий kubeconfig context**. Выберите тестовый кластер.

```sh
make install
# В отдельном терминале; заранее подготовьте release image, Secrets, БД/Redis и StorageClass.
make run
kubectl apply -k config/samples
kubectl get bitrixclusters
kubectl get bitrixcluster example -o yaml
```

Пример содержит ссылки на Secrets и внешние DB/Redis, но credentials в Git не хранит.
Release image, StorageClass и endpoints нужно заменить своими значениями. `sites[].tls` необязателен; наличие HTTPS listener
проверяется контроллером Gateway, а не текущей схемой.

Чтобы запустить manager в кластере, соберите собственный образ:

```sh
make docker-build docker-push IMG=registry.example.com/bitrix-operator:dev
make deploy IMG=registry.example.com/bitrix-operator:dev
```

`controller:dev` в Kustomize — placeholder, опубликованного образа пока нет.
Manager использует leader election, non-root, read-only root filesystem, seccomp,
health probes и защищённые metrics. Admission webhook и cert-manager не требуются.
По умолчанию metrics используют self-signed TLS; для production задаётся сертификат.

## Границы текущей итерации

CI: форматирование, Go modules, build/vet/lint, API contract, envtest на 1.36.0/1.37.0,
повторная генерация CRD/RBAC/deepcopy и сборка Kustomize. Добавлены native nginx/PHP
проверки и kind E2E для web/storage с внешними MySQL/PostgreSQL/Redis.
Cron/agents реализованы отдельным singleton Deployment с native PHP scheduler.
Требования к RWX, подготовке Bitrix и гарантии блокировки — [cron и agents](docs/cron-agents.md).
Push publisher/subscriber, nginx WebSocket/long polling, signed REST и ротация ключа
реализованы; [включение и границы доверия](docs/push-pull.md).
Реализованы Host → SITE_ID, validation Jobs и проверка текущего runtime перед Ready;
[контракт и ограничения проверки сайтов](docs/sites.md).
Реализованы HTTPRoute, Gateway/TLS, проверки сертификатов и ReferenceGrant;
[подключение и ограничения публикации](docs/gateway-tls.md).
Добавлен HPA v2 с передачей владения replicas и проверкой requests/RWX;
[настройка и ограничения autoscaling](docs/autoscaling.md).
Добавлены web PDB, предпочтительное размещение по узлам и согласованное завершение nginx/PHP;
[обслуживание узлов и ограничения](docs/disruptions.md).
Добавлены метрики BitrixCluster, Events и HTTPS-профиль Prometheus с alerts/dashboard;
[наблюдаемость](docs/observability.md) и [диагностика](docs/operations.md).
Security review, NetworkPolicy overlays и публикация operator image — следующие этапы.
См. также [web workloads](docs/web-workloads.md).

Лицензия: [Apache-2.0](LICENSE).

## Runtime contract (итерация 2)

Добавлены renderer nginx/PHP/Bitrix, PHP-only Secret projections, подготовка
release image и Docker-проверки конфигураций. Подробности и ограничения —
[docs/runtime-contract.md](docs/runtime-contract.md).

Конфигурации компонентов находятся в [internal/runtime/templates](internal/runtime/templates/README.md).
Go выполняет подстановку проверенных параметров; нативные PHP-файлы копируются без обработки.
