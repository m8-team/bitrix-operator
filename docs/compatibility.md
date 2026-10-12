# Проверенная совместимость

Матрица описывает конкретные проверки репозитория на этапе 8.3a. Это не обещание
совместимости со всеми сочетаниями версий. Закреплённые значения находятся в
`go.mod`, Dockerfile, Makefile, workflow/helper scripts и test fixtures.

| Компонент | Проверяемая версия / режим | Доказательство и граница |
|---|---|---|
| Kubernetes API | 1.36.0, 1.37.0 | `Tests`: envtest обеих версий; реальные CRD/CEL/reconcile. Полные kind E2E — 1.37.0 |
| Manager architectures | Linux amd64, arm64 | `Release images`: native build/startup + SPDX/Trivy. Полный workload kind E2E выполняется на amd64 |
| Go / controller-runtime | 1.26.8 / 0.25.0 | Lint, build, module verification, unit/envtest; grpc-go исправлен до 1.83.2 |
| Kubebuilder / controller-gen | 4.16.0 / 0.22.0 | Scaffold metadata и повторная генерация API manifests |
| Kustomize / Helm | 5.8.1 / 4.3.0 | Render и `Install artifacts`: install/upgrade/rollback/uninstall/reinstall |
| nginx / PHP-FPM | Bitrix images 1.30.5-v1-alpine / 8.2.34-fpm-v1-alpine | Digests в sample и runtime contract; native config tests + fixture HTTP/runtime E2E |
| MySQL | 8.4.11 | External DB fixture: подключение, outage, Secret rotation, logical backup/restore |
| PostgreSQL | 17.11-trixie | Аналогичные fixture-проверки; совместимость лицензированного ядра Bitrix не подтверждена |
| Redis | 8.2.10-alpine | External fixture: cache/sessions/Push, outage и credential rotation |
| Push image | 3.3-v1-alpine; upstream server 0.6.0 | REST/WebSocket/long polling, signed channels, Redis, ingress isolation; reconnect при restart |
| Gateway API / controller | 1.6.2 / Envoy Gateway 1.9.2 | HTTP/HTTPS, certificates, ReferenceGrant, hostname conflicts и rotation |
| Metrics API | metrics-server 0.9.0 | HPA fixture: нагрузка и missing metrics. Insecure kubelet TLS только в disposable kind |
| NetworkPolicy | Cilium 1.20.2 | Enforcement E2E: positive/negative probes, cross-node и пересоздание Pods |
| Storage / recovery | kind local-path + fixture RWX contract | Сохранение CR/PVC объектов, uploads и logical DB drill. Production CSI не сертифицирован |
| Real Bitrix | Пользовательский licensed release | Требуется отдельный smoke на доступном лицензированном окружении; fixture его не заменяет |

Результаты конкретного коммита смотрите в GitHub Actions: зелёный PR run не является
доказательством для другого SHA. Перед release policy требует успешный main CI
именно на SHA тега. Подробности — [release process](releases.md).

## Upgrade matrix

| Переход | Состояние | Что фактически проверено |
|---|---|---|
| Установка из source-generated YAML в чистый kind | Проверяется в CI | CRD/RBAC/manager, readiness процесса |
| Helm install → upgrade → rollback той же версии | Проверяется в CI | 1→2→1 manager replicas, install lifecycle; не смена версии API/бинарника |
| Helm uninstall → reinstall | Проверяется в CI | Сохранение UID существующих CRD/CR/PVC |
| CRD update существующей установки | Отдельное действие администратора | Helm `crds/` не выполняет upgrade; схема требует review/migration plan |
| Предыдущий опубликованный RC → следующий RC | **Открыто** | Нужны два реально опубликованных digest, тест upgrade и rollback на данных |
| Переход между Kubernetes minor | **Открыто для running cluster upgrade** | Два envtest API server подтверждают работу по отдельности, не rolling cluster upgrade |
| Обновление Bitrix/PHP/DB | **Зависит от конкретного release** | Manager rollout не откатывает database migrations и данные |

Первый RC ещё не опубликован. Нельзя считать тест изменения числа replicas
проверкой upgrade между версиями оператора. До закрытия этих строк production
выкатка требует собственных staging/backup/restore проверок.

## Как обновлять матрицу

Каждый dependency PR проходит [maintenance procedure](dependency-maintenance.md).
Изменение версии добавляется в матрицу после соответствующего успешного теста.
Для реального Bitrix smoke фиксируются версия/редакция ядра, release digest,
DB/PHP/storage версии, сайт(ы), проверенные сценарии и ссылка на результаты без
лицензий, паролей и персональных данных. Минимум: login, страницы обоих сайтов,
upload/download, agents, cache/sessions, Push при включении, HTTPS и rollback
с заранее определёнными границами data migrations.
