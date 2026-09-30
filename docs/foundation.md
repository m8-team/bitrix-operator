# Итерация 1: API foundation

Дата: 2026-09-29. Ветка: `feat/01-foundation`.

## Происхождение scaffold и версии

Scaffold создан настоящим CLI:

```sh
kubebuilder init --domain m8.team \
  --repo github.com/m8-team/bitrix-operator --owner 'M8 Team' \
  --plugins go/v4 --fetch-deps=false
kubebuilder create api --group bitrix --version v1alpha1 \
  --kind BitrixCluster --resource=true --controller=true --make=false
```

После scaffold перенесён исходный API, добавлен адаптер SchemeBuilder,
сгенерированы deepcopy/CRD/RBAC и реализован foundation status.

| Компонент | Закреплённая версия |
| --- | --- |
| Kubebuilder | v4.16.0, release 2026-09-10 |
| Plugin / PROJECT | go/v4 / version 3, cliVersion 4.16.0 |
| Go | 1.26.8 |
| controller-runtime / setup-envtest | v0.25.0 |
| k8s.io/api, apimachinery, client-go | v0.37.0 |
| controller-tools | v0.22.0 |
| Kustomize | v5.8.1 |
| golangci-lint | v2.13.1 |
| logcheck module | sigs.k8s.io/logtools v0.10.1 |
| envtest default / CI matrix | 1.37.0 / 1.36.0 и 1.37.0 |

Go release archive SHA-256 (linux/amd64):
`d0f743b33e8d8945e6b1f432edd15785c70507121d6e2a723b21285eddf8b57b`.

Kubebuilder binary SHA-256 (linux/amd64):
`539984e58832044f2326b1bb940c1eaf784f1849e65d06d3a4a4a8b5dfac7ca7`.

SHA-256 исходного BitrixCluster:
`93776bb0de07729ea8b6e3aa78815564f3b99c52ec6bae43d78314d7cf51cd80`.

Проверенные источники:
- https://github.com/kubernetes-sigs/kubebuilder/releases/tag/v4.16.0
- https://github.com/kubernetes-sigs/kubebuilder/blob/v4.16.0/testdata/project-v4/go.mod
- https://go.dev/dl/?mode=json
- https://github.com/kubernetes-sigs/controller-runtime/tree/v0.25.0/pkg/envtest

Dependency versions проверяются по scaffold и закрепляются в go.mod/go.sum.
GitHub Actions закреплены полными commit SHA. CI использует exact envtest versions,
а не «первый найденный» бинарник из bin/k8s. Год в generation фиксирован на 2026,
чтобы regenerated files не менялись только из-за смены календарного года.

## Что проверяют тесты

- Побайтовое сохранение исходного API, omitzero, duration JSON и DeepCopy isolation.
- Реальная установка CRD в kube-apiserver, обязательные поля, enums и CEL.
- Минимальный HTTP sample без TLS и optional components, defaults replicas/pullPolicy.
- Ровно одно default connection, ports, PVC/EmptyDir, cron/gateway enabled-only поля.
- HPA bounds и отключённые optional objects без вложенных обязательных при enabled полей.
- PostgreSQL, TLS Secret reference и duration round-trip.
- Отдельный status subresource и защита spec от status update.
- Pending/Ready=False, отсутствие ложного release status, idempotency и generation.
- Запуск manager и reconciliation через watch без Gateway API CRDs.

## Границы валидации исходного контракта

Go type `metav1.Duration` отвергает неверный формат при typed JSON decoding.
Схема CRD не содержит собственного правила положительного интервала; нулевые и
отрицательные duration требуют runtime-проверки на этапе cron. Тесты не должны
создавать ложное впечатление, что все семантические ограничения уже проверяет API server.

TLS Secret existence, listener protocol, DB/Redis доступность, image validity и
site existence не проверяются foundation-контроллером. Accepted YAML не означает
готовность Bitrix. Не добавлены новые поля, webhook, Gateway-зависимость или права
доступа к пользовательским Secret values.

## Результаты выполнения

Локально выполнено (Linux amd64):

| Проверка | Результат |
| --- | --- |
| make build / go vet | успешно |
| make test, envtest 1.37.0 | 22/22 integration specs + 3 API unit tests |
| make test ENVTEST_K8S_VERSION=1.36.0 | 22/22 integration specs + 3 API unit tests |
| make lint-config lint / lint-fix | 0 issues |
| go mod tidy / go mod verify | успешно, modules verified |
| CRD, RBAC, deepcopy regeneration | успешно, повторная генерация без diff |
| Kustomize CRD/default/samples | успешно |
| Контракт API SHA-256 | совпадает с исходным файлом |

Coverage пакета controller — 92.9%; это покрытие foundation, не будущего runtime.
GitHub Actions повторяет обязательные проверки на PR; статус доступен в Checks.
Матрица envtest относится к API foundation; реальный кластер/kind, container build,
Bitrix, storage driver и Gateway ещё не сертифицированы.
