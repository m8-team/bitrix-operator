# Проверка NetworkPolicy на Cilium

Итерация 7.4.2 добавляет отдельный CI workflow `Network policy enforcement` и
`bash hack/e2e-network.sh`. Результат конкретного запуска смотрите в GitHub Actions:
пока job не завершён успешно, наличие сценария не означает подтверждённый enforcement.

## Что проверяется

Создаётся новый двухузловой kind-кластер без default CNI; устанавливается Cilium.
Используются настоящие `config/security` Kustomize profiles и `workload.PushPolicy`.
У синтетических Push policies удаляются только ownerReferences, поскольку BitrixCluster
и controller в этом отдельном тесте не устанавливаются; selectors/rules не изменяются.

Небольшой Go listener/client в scratch image получает те же workload role/UID labels.
Все проверяемые TCP ports действительно слушают, поэтому закрытый сервер не может
выглядеть как работающая сетевая политика. Listener не содержит PHP/Bitrix, БД, Redis,
Gateway implementation или настоящего Prometheus; это тест сетевого доступа к ролям.
Существующий `Web workloads` проверяет работу runtime отдельно.

Порядок проверок:

1. До применения policies каждое соединение будущей матрицы должно успешно дойти
   до listener через ClusterIP Service и получить известный ответ.
2. Применяются workload/manager profiles и operator Push policies. Проверяются
   Gateway → web, web/cron/validation → DB, все runtime роли → Redis, собственный
   Push, запрет чужого UID и прямого Gateway → Push, namespace AND Pod selectors,
   Prometheus → manager и запрет других источников/портов.
3. DNS `kubernetes.default.svc.cluster.local` разрешается через настоящий CoreDNS
   отдельно по UDP и TCP из пяти runtime ролей.
4. PostgreSQL overlay заменяет разрешённый DB port 3306 на 5432. Проверяется новый
   доступ и блокировка прежнего порта без перезапуска listener.
5. Web Deployment переносится с worker на control-plane, проверяется новый UID,
   nodeName и Ready Pod. Матрица и DNS повторяются. Затем Pod удаляется и проверяются
   новый UID, готовность и соединения после автоматического пересоздания.

Gateway/DB/Redis сначала размещены на control-plane, web/Push — на worker: есть
реальный cross-node трафик. После переноса web часть потоков становится локальной,
а соединения к Push продолжают проходить между узлами.

## Защита от ложного результата

TCP пробы используют числовые Service IP, не DNS names. DNS проверяется отдельно.
Allow требует TCP connect и точного ответа fixture. Deny требует три последовательных
таймаута новых dial; connection-refused, неправильный ответ, ошибка DNS/exec или
неработающий Pod не принимаются за успешный deny. Повторы учитывают convergence
политик/Cilium endpoints. Unit test проверяет различие timeout/refused/broken responder.
После удаления Pod ожидается фактический новый Ready UID, а не только Deployment status.

Неразрешённый egress проверяется к постороннему Service в другом namespace. Публичный
Интернет и внешние CIDR здесь не тестируются. Manager egress остаётся открытым,
как явно предусмотрено профилем. Этот тест не проверяет TLS, authentication, содержимое
приложения или бизнес-операции Bitrix.

## Версии и локальный запуск

| Компонент | Зафиксированная версия |
|---|---|
| kind | 0.33.0, бинарник с SHA-256 из существующей test matrix |
| Kubernetes node | 1.37.0, image digest в скрипте |
| Cilium | 1.20.2, SHA-256 Helm chart из официального index.yaml |
| Cilium CLI | 0.20.1, SHA-256 release asset |
| Fixture | Go из go.mod, CGO=0, scratch image, non-root UID 65532 |

Helm values находятся в родном YAML `test/e2e/network/cilium-values.yaml`.
Сохраняется kube-proxy, IPAM Kubernetes, один Cilium operator, без Envoy/L7.
Закреплённый chart содержит image digests; они не отключаются. CLI и kind устанавливает
workflow с проверкой checksums; для локального запуска предоставьте те же версии.

Нужны Linux, Docker, kind, kubectl, Cilium CLI, Go, make, Python 3, curl, tar и sha256sum.
Запуск из корня репозитория:

```sh
bash hack/e2e-network.sh
```

Скрипт всегда создаёт новый cluster с уникальным именем и отдельным временным kubeconfig.
Текущий kubeconfig пользователя не используется. Существующий cluster не переиспользуется.
После завершения удаляются только созданный cluster и его временные файлы; при ошибке
сначала печатаются Pods, NetworkPolicies и диагностика Cilium. Fixture image остаётся
в локальном Docker cache, registry push отсутствует. Реальные пользовательские данные
и credentials не нужны.

Границы подтверждения: только закреплённый kind/Cilium, IPv4 Service dataplane и
описанные сценарии. Другие CNI, NodeLocal DNS, hostNetwork, IPv6, external DB failover,
L7/TLS и CSI требуют своих испытаний. Успех нельзя выдавать за сертификацию любого
production-кластера. См. [профили и границы доверия](security.md).

Источники версий и установки (2026-10-07):

- [Cilium kind installation](https://docs.cilium.io/en/stable/installation/kind/).
- [Cilium v1.20.2](https://github.com/cilium/cilium/releases/tag/v1.20.2).
- [Cilium CLI v0.20.1](https://github.com/cilium/cilium-cli/releases/tag/v0.20.1).
- [Helm chart index](https://helm.cilium.io/index.yaml).
