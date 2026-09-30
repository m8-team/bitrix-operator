# ADR 0001: API foundation без изменения BitrixCluster

Статус: принято для итерации 1. Дата: 2026-09-29.

## Контекст

Предоставлен полный Go-контракт `BitrixCluster`, но без регистрации API group,
проекта Kubebuilder и сгенерированного CRD. Нужно получить проверяемую основу
оператора, сохранив поля, JSON names, markers и поведение optional TLS.

## Решения

1. Namespaced API `bitrix.m8.team/v1alpha1`; один Kind `BitrixCluster`.
2. Scaffold создан Kubebuilder v4.16.0, `go/v4`, `PROJECT` version 3. Go 1.26.8.
3. API source остаётся побайтово идентичным входному файлу. Архивная копия получает
   расширение `.go.txt`, чтобы Go и controller-gen не компилировали её как второй API package.
4. Новый scaffold использует `runtime.SchemeBuilder.Register(func)`, а исходный контракт
   вызывает `Register(objects...)`. Маленький адаптер в `groupversion_info.go`
   сохраняет этот вызов, используя только apimachinery. Deprecated helper
   controller-runtime/pkg/scheme не используется.
5. Defaulting/CEL выполняет API server. Webhook не добавляем; он не нужен для foundation.
6. Reconcile читает CR и патчит только status с optimistic locking. Статус Pending и
   Ready=False честно отражает отсутствие runtime. Повторный reconcile не пишет тот же
   status и не меняет LastTransitionTime без изменения состояния Condition.
7. Manager RBAC ограничен чтением CR и обновлением status; нет прав на Secrets,
   workloads, PVC, CR spec и finalizers до появления соответствующей реализации.
8. envtest проверяет реальную установку CRD и watch manager. API server слушает локально,
   advertise-address=127.0.0.1 и endpoint-reconciler-type=none позволяют запуск
   в среде без default route. Kubernetes Service endpoints не нужны envtest;
   admission, CEL и status subresource продолжают работать.
9. Generated files коммитятся; CI выявляет diff и новые несохранённые generated files.

## Следствия

Foundation можно собирать и проверять независимо от Bitrix images, Gateway CRDs,
cert-manager, MySQL и Redis. Это не доказательство готовности портала.
Runtime-проверки, особенно HTTPS listener, существование site ID, secrets и storage,
будут добавлены в соответствующих итерациях без расширения spec.
