# Bitrix Operator

Bitrix Operator запускает и обслуживает коробочный портал или сайты «1С-Битрикс» в Kubernetes. Вы описываете приложение в ресурсе `BitrixCluster`: образ с кодом, домены сайтов, подключения к БД и Redis, хранилище и нужные компоненты. Оператор создаёт ресурсы Kubernetes и обновляет их при изменении настроек.

Проект рассчитан на установку, в которой код приложения собирается в контейнерный образ, а база данных, Redis и файловое хранилище обслуживаются отдельно. Установку Bitrix и обновления ядра нужно выполнять при подготовке приложения. Код в работающих контейнерах доступен только для чтения.

## Возможности

- Запуск nginx и PHP-FPM, проверка доступности БД и Redis, обновление контейнеров при смене образа или Secrets.
- Несколько сайтов с выбором `SITE_ID` по домену. Сайты используют общий код и каталог загрузок.
- Постоянное хранилище для `upload`. При удалении `BitrixCluster` PVC с загрузками сохраняется.
- Отдельный процесс для запуска агентов по расписанию и сервер Push & Pull.
- Публикация сайтов через Gateway API с завершением TLS на Gateway.
- Автомасштабирование web-компонента через HPA, PodDisruptionBudget и корректное завершение nginx/PHP при остановке Pod.

Агенты, Push, Gateway и автомасштабирование включаются отдельно. Для внутреннего HTTP-доступа Gateway не нужен.

## Перед запуском

Подготовьте:

- Kubernetes-кластер и доступ к нему через `kubectl`.
- Образ с готовым приложением Bitrix. Требования к структуре образа и подготовке PHP-настроек описаны в [контракте runtime](docs/runtime-contract.md).
- Доступные из кластера MySQL или PostgreSQL и Redis с паролем.
- StorageClass для пользовательских загрузок. Несколько web-реплик требуют `ReadWriteMany`; с `ReadWriteOnce` или `ReadWriteOncePod` поддерживается одна реплика, а обновления проходят с простоем.
- Secrets с лицензией, ключом Bitrix Crypto и паролями подключений в том же namespace, что и `BitrixCluster`.

Для внешнего HTTP/HTTPS-доступа дополнительно нужны Gateway API CRDs, работающий Gateway controller и настроенный Gateway. DNS, сертификаты и разрешения между namespaces подготавливаются отдельно. Подробности — в [инструкции по Gateway и TLS](docs/gateway-tls.md).

Проект находится в разработке. Публичный образ оператора пока не опубликован — его нужно собрать самостоятельно. Перед использованием с рабочими данными проверьте свой образ Bitrix, модули приложения и storage driver в тестовом окружении. Тесты проекта используют синтетическое приложение и не подтверждают совместимость любой сборки Bitrix.

## Установка оператора

Для сборки нужны Go **1.26.8**, Docker, make и git. Инструменты генерации и Kustomize устанавливаются через Makefile; потребуется доступ к Go modules и GitHub releases.

```sh
git clone https://github.com/m8-team/bitrix-operator.git
cd bitrix-operator

# Укажите свой registry и тег образа.
export IMG=registry.example.com/bitrix-operator:dev
make docker-build docker-push IMG="$IMG"
```

Убедитесь, что выбран нужный Kubernetes-кластер, затем установите оператор:

```sh
kubectl config current-context
make deploy IMG="$IMG"
kubectl -n bitrix-operator-system rollout status deployment/bitrix-operator-controller-manager
```

`make deploy` устанавливает CRD и оператор в namespace `bitrix-operator-system`. Узлам кластера должен быть доступен собранный образ. Для приватного registry отдельно настройте доступ к нему. Admission webhook и cert-manager для самого оператора не требуются.

## Запуск приложения

В [примере BitrixCluster](config/samples/bitrix_v1alpha1_bitrixcluster.yaml) показана установка с внутренним HTTP-доступом, MySQL, Redis и RWX-хранилищем. Скопируйте пример и замените образ приложения, домен, адреса сервисов, имена Secrets и StorageClass:

```sh
cp config/samples/bitrix_v1alpha1_bitrixcluster.yaml bitrixcluster.yaml
kubectl create namespace bitrix
```

Создайте необходимые Secrets в namespace `bitrix` своим обычным способом управления секретами. Не сохраняйте их значения в манифесте приложения или Git. После настройки примените файл:

```sh
kubectl -n bitrix apply -f bitrixcluster.yaml
kubectl -n bitrix get bitrixclusters
kubectl -n bitrix wait bitrixcluster/example --for=condition=Ready --timeout=300s
```

В примере ресурс называется `example`. Его HTTP Service — `example-web`, порт 80. Для проверки внутреннего доступа:

```sh
kubectl -n bitrix port-forward service/example-web 8080:80
```

В другом терминале отправьте запрос с доменом из `spec.sites[].hosts`:

```sh
curl -H 'Host: portal.example.com' http://127.0.0.1:8080/
```

Домен должен соответствовать сайту, который уже существует в Bitrix. Оператор проверяет указанные site IDs через ядро приложения; неизвестные домены nginx не обслуживает.

## Проверка состояния

Если приложение не выходит в готовность, посмотрите условия и события ресурса:

```sh
kubectl -n bitrix describe bitrixcluster example
kubectl -n bitrix get bitrixcluster example -o yaml
kubectl -n bitrix-operator-system logs deployment/bitrix-operator-controller-manager -c manager
```

`WebReady` показывает готовность web-реплик и хранилища. Проверка внутри Pod включает подключения к БД и Redis. Общий `Ready` также учитывает проверку сайтов и готовность включённых компонентов. Готовность Gateway не подтверждает, что DNS настроен и сайт доступен из внешней сети.

Изменяйте настройки через `BitrixCluster`: ручные изменения образов, конфигурации или числа реплик управляемых Deployment оператор восстановит. При включённом HPA число web-реплик задаёт автомасштабирование.

## Данные и ограничения

Удаление `BitrixCluster` сохраняет PVC с загрузками, но не заменяет резервное копирование. Новый ресурс с тем же именем не присваивает старый PVC автоматически. Восстановление данных нужно планировать отдельно; [правила работы с хранилищем](docs/web-workloads.md#storage-и-жизненный-цикл) описывают этот случай.

Оператор не устанавливает Bitrix, не выполняет миграции БД и не создаёт резервные копии. TLS к внешним БД и Redis пока не поддерживается. Сайты разделяются по доменам; переключение по URL-папкам не реализовано. Для агентов нужна общая RWX-файловая система с работающей межузловой блокировкой `flock` и предварительная настройка Bitrix.

## Настройка компонентов

| Задача | Документация |
| --- | --- |
| Подготовить образ приложения и PHP-настройки | [Runtime contract](docs/runtime-contract.md) |
| Настроить web и хранилище загрузок | [Web workloads](docs/web-workloads.md) |
| Подключить сайты и проверить site IDs | [Сайты](docs/sites.md) |
| Запускать агенты по расписанию | [Cron и агенты](docs/cron-agents.md) |
| Включить Push & Pull | [Push & Pull](docs/push-pull.md) |
| Опубликовать сайты по HTTP/HTTPS | [Gateway и TLS](docs/gateway-tls.md) |
| Настроить автомасштабирование | [HPA](docs/autoscaling.md) |
| Настроить поведение при обслуживании узлов | [PDB и завершение запросов](docs/disruptions.md) |

## Разработка

API: `bitrix.m8.team/v1alpha1`, ресурс `BitrixCluster` в пределах namespace.

```sh
make build
make test
make lint-config lint
make verify-generated
```

`make test` запускает локальные etcd и kube-apiserver через envtest; Docker для этих тестов не нужен. Проверки выполняются на Kubernetes 1.37.0. Для предыдущей версии:

```sh
make test ENVTEST_K8S_VERSION=1.36.0
```

Для запуска оператора с локальной машины используйте `make install` и `make run` с kubeconfig тестового кластера. Проверки web-компонентов в отдельном Kind-кластере запускаются через `bash hack/e2e-web.sh`; требования перечислены в [документации](docs/web-workloads.md#проверки-и-границы).

Закреплённые версии инструментов и устройство проекта описаны в [foundation](docs/foundation.md). Дальнейшие задачи — в [плане разработки](docs/implementation-plan.md).

## Лицензия

[Apache-2.0](LICENSE). Лицензия на само приложение Bitrix приобретается отдельно.
