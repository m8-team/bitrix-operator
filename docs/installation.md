# Установка и жизненный цикл оператора

Итерация 8.1 добавляет `dist/chart` и `dist/install.yaml`. Chart создан командой
`kubebuilder edit --plugins=helm/v2-alpha` с Kubebuilder 4.16.0, затем адаптирован
к существующему controller. Проверки выполняются Helm **4.3.0** и Kubernetes
**1.36/1.37** (render), kind **1.37.0** (lifecycle E2E).

Версия chart — `0.1.0-dev`. Публичного release image/OCI chart пока нет; это
установочные исходники, а не объявление production release. Для установки нужен
собственный образ manager, собранный из того же commit. Образы Bitrix, БД, Redis,
Gateway, CSI и backup инфраструктура устанавливаются отдельно.

## Helm

Из корня checkout:

```sh
bash hack/install-helm.sh
make docker-build docker-push IMG=registry.example.com/team/bitrix-operator:my-build
bin/helm install bitrix-operator ./dist/chart \
  --namespace bitrix-operator-system --create-namespace \
  --set-string manager.image.reference=registry.example.com/team/bitrix-operator:my-build \
  --wait --timeout 5m
```

Для private registry задайте `manager.imagePullSecrets` в values-файле и заранее
создайте pull Secret в namespace manager. Поле `manager.image.reference` принимает
полную ссылку, включая registry port или `repository@sha256:<digest>`; digest
предпочтителен для фиксированного развертывания. Пустое значение отклоняется.
`make helm-deploy IMG=...` передаёт ту же полную ссылку без разбиения по двоеточию.

Одна установка operator обслуживает весь кластер. Размещение нескольких releases
в разных namespaces не разделяет наблюдаемые CR: namespace-only watch пока нет,
поэтому `rbac.namespaced=true` отклоняется. Service metrics выбирает только Pods
с label своего Helm release, но это не создаёт tenant isolation controller.

Chart устанавливает manager, ServiceAccount, RBAC и HTTPS metrics Service.
Default Pod security: non-root, RuntimeDefault seccomp, read-only root filesystem,
drop ALL capabilities; default leader election включён. API token нужен manager
для Kubernetes API. Внешний ServiceAccount выбирается через `serviceAccount.enabled=false`
и обязательное `serviceAccount.name`; bindings создаются для него.

Опции `metrics.enabled`, `metrics.port`, `metrics.secure` и
`manager.healthProbe.port` управляют endpoint/probes. HTTPS по умолчанию использует
self-signed certificate controller-runtime. Для production мониторинга подготовьте
доверенный сертификат/CA и reader RBAC; chart не устанавливает Prometheus или
cert-manager. [Monitoring profile](observability.md) и [NetworkPolicy](security.md)
применяются отдельно с адаптацией namespace/labels к выбранному release.
Webhook в этой версии отсутствует; соответствующие values отклоняются.

## CRD и обновление

Chart содержит **точную копию** сгенерированного CRD в `crds/`, без Helm templates.
Helm устанавливает CRD при первой установке, пропускает существующий и не удаляет
его при uninstall. Helm также **не обновляет** CRD при upgrade/rollback.

Перед обновлением manager:

1. Сохраните проверенный backup и прочитайте изменения API/версий. Сравните CRD
   со схемой установленной версии и проверьте совместимость существующих CR.
2. Примените CRD из того же checkout/release, который устанавливаете:

   ```sh
   kubectl apply --server-side -f dist/chart/crds/
   ```

   Не обходите field ownership conflict через `--force-conflicts` вслепую:
   определите владельца CRD и согласуйте управление им.
3. Обновите operator и дождитесь готовности:

   ```sh
   bin/helm upgrade bitrix-operator ./dist/chart \
     --namespace bitrix-operator-system \
     --set-string manager.image.reference=registry.example.com/team/bitrix-operator:new-build \
     --wait --timeout 5m
   ```

Используйте полный проверенный values-файл для каждого upgrade. `helm rollback`
возвращает предыдущие chart values/resources, но не откатывает CRD, release image
Bitrix, БД, migrations или uploads. Установка старого manager допустима только при
совместимости с текущими CRD/spec; иначе сначала нужен проверенный recovery plan.

```sh
bin/helm history bitrix-operator -n bitrix-operator-system
bin/helm rollback bitrix-operator 1 -n bitrix-operator-system --wait --timeout 5m
```

## Удаление и повторная установка

```sh
bin/helm uninstall bitrix-operator -n bitrix-operator-system --wait
```

Удаляется manager и chart-owned RBAC/Service, сохраняются CRD, BitrixCluster и
uploads PVC. Уже созданные workloads не принадлежат Helm release; без manager они
перестают reconciliate. Для удаления приложения используйте отдельную процедуру
его остановки и удаления CR с проверкой сохранности данных. Не удаляйте CRD или
namespace с пользовательскими данными ради удаления только оператора.

Повторная установка того же chart в тот же namespace использует существующий CRD.
Для перехода с Kustomize на Helm сначала остановите прежний manager и согласуйте
RBAC/resource ownership; автоматический takeover этим chart не реализован.

## Установочный YAML

```sh
make build-installer IMG=registry.example.com/team/bitrix-operator:my-build
kubectl apply -f dist/install.yaml
kubectl -n bitrix-operator-system rollout status deployment/bitrix-operator-controller-manager
```

Сборка работает на временной копии `config/`, поэтому IMG не изменяет исходный
`config/manager/kustomization.yaml`. В Git лежит baseline с `controller:dev` — его
нужно пересобрать со своим образом. Bundle включает CRD и Namespace:
**не используйте `kubectl delete -f dist/install.yaml` для удаления только manager**,
это удалит и CRD. В lifecycle E2E полное удаление bundle выполняется только в новом
пустом кластере до создания data fixtures.

## Разработка и проверки

```sh
make sync-chart
make test-chart
make build-installer IMG=controller:dev
bash hack/e2e-chart.sh
```

`make sync-chart` копирует generated CRD/RBAC из `config/`; RBAC template читает rules
из копии YAML. Тесты сравнивают эти файлы побайтово и rendered RBAC с исходным.
Изменения runtime templates Go генерацией не заменяются.
Не запускайте повторно Helm plugin с `--force` без review: он перезапишет наши
values/schema, CRD lifecycle, selectors и workflow. Обновляйте scaffold отдельно,
с сохранением адаптаций и проверкой diff; `PROJECT` меняет только Kubebuilder CLI.

Workflow `Install artifacts` проверяет lint/render, синхронизацию generated files,
установку Kustomize bundle и установку **упакованного** Helm `.tgz`, upgrade до двух
manager replicas, rollback, uninstall и reinstall. Сохраняются UID CRD/CR/PVC.
PVC fixture намеренно unbound: это проверка lifecycle объектов; реальное сохранение
и восстановление uploads проверяются [recovery drill](recovery.md).
В этой итерации upgrade меняет конфигурацию одного build, а не доказывает
совместимость двух разных релизов. Такая матрица остаётся следующим gate.

Сценарий создаёт новый kind и private kubeconfig, не использует текущий context и
удаляет только созданный кластер. Он не публикует images или chart в registry.

Источники: [Kubebuilder Helm plugin](https://book.kubebuilder.io/plugins/helm-v2-alpha),
[Helm CRD lifecycle](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/),
[Helm 4.3.0](https://github.com/helm/helm/releases/tag/v4.3.0).
