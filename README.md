# Bitrix Operator

Kubernetes operator для коробочного портала и сайтов Bitrix на основе предоставленного
`BitrixCluster` API.

**Текущий этап: итерация 0 — план разработки. Controller пока не реализован.**

- [План, архитектура и критерии приемки](docs/implementation-plan.md)
- [Исходный BitrixCluster API без изменений](docs/reference/bitrixcluster_types.go)

Технологическая база: Kubebuilder v4.16.0, plugin go/v4; версии проверены 2026-09-29.
Перед scaffold проверяется актуальная stable version и закрепляются зависимости.

Оператор будет управлять web, cron/agents, Push & Pull, uploads PVC, конфигурациями,
HPA и HTTPRoute. БД, Redis, Gateway и TLS Secrets предоставляются инфраструктурой.

Разработка идет отдельными итерациями и PR согласно плану.
Следующий этап: Kubebuilder scaffold, генерация CRD, envtest и CI.
