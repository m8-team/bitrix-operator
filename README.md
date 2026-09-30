# Bitrix Operator

Kubernetes operator для коробочного портала и сайтов Bitrix.

**Итерация 1: API foundation.** Manager наблюдает `BitrixCluster` и выставляет
`Pending`, `Ready=False`, reason `RuntimeNotImplemented`. Web, cron, Push & Pull,
PVC и маршруты пока не создаются. Установка manager не запускает Bitrix.

## API и разработка

- API: `bitrix.m8.team/v1alpha1`, namespaced `BitrixCluster`.
- [План итераций](docs/implementation-plan.md).
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
# В отдельном терминале; приложение Bitrix не разворачивается.
make run
kubectl apply -k config/samples
kubectl get bitrixclusters
kubectl get bitrixcluster example -o yaml
```

Пример содержит ссылки на Secrets и внешние DB/Redis, но credentials в Git не хранит.
Образы приложения, StorageClass и endpoints — примеры; их runtime-совместимость будет
проверяться в следующих итерациях. `sites[].tls` необязателен; наличие HTTPS listener
будет проверяться контроллером на этапе Gateway, а не текущей схемой.

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
повторная генерация CRD/RBAC/deepcopy и сборка Kustomize. Контейнерный kind E2E scaffold
сохранён под build tag `e2e`; CI пока его не запускает. Runtime и публикация образов —
следующие итерации. См. [результаты проверок](docs/foundation.md).

Лицензия: [Apache-2.0](LICENSE).
