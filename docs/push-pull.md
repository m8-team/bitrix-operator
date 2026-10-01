# Push & Pull (итерация 5)

`spec.runtime.push.enabled=true` включает отдельные publisher/subscriber
Deployments, ClusterIP Services и ingress NetworkPolicy. Существующий BitrixCluster
и его CRD не изменены. Redis остаётся внешним, используется `spec.redis`.

## Включение

Сначала подготовьте Secret в namespace BitrixCluster: ключ подписи — случайная
строка не менее 32 и не более 512 байт UTF-8 (например, 32 random bytes в hex).
Оператор не создаёт этот Secret. Значение не должно попадать в Git или командные логи.
Примените [пример merge patch](../config/examples/push-patch.yaml):

```sh
kubectl -n YOUR_NAMESPACE patch bitrixcluster YOUR_CLUSTER --type=merge \
  --patch-file config/examples/push-patch.yaml
kubectl -n YOUR_NAMESPACE wait bitrixcluster/YOUR_CLUSTER --for=condition=PushReady --timeout=300s
```

В примере закреплён `quay.io/bitrix24/push:3.3-v1-alpine` по digest;
проверенный runtime — upstream Push server 0.6.0, Node 22, UID/GID 979.
`release.imagePullSecrets` также используются для Push. Publisher и subscriber
масштабируются независимо через существующие `replicas`; HPA пока не реализован.

`PushReady=True` требует актуальную observed generation и все ready/updated/available
реплики обоих Deployments. `WebReady` проверяется отдельно. Общий `Ready` остаётся
False до реализации проверки site IDs через ядро Bitrix.

## Конфигурации и протокол

Родные `publisher.json`, `subscriber.json`, PHP `pull.php`, Node helpers и shell
entrypoint находятся в `internal/runtime/templates/push`. Go сериализует только
manifest со ссылками/параметрами. В Pod Node читает JSON и Secret files и сериализует
результат в `/tmp/push/config.json` (0600, memory EmptyDir 128Mi). Подстановки секретов
через shell/envsubst нет. Ошибка чтения выдаёт только общую диагностику.

Push Pod не получает release-код, uploads PVC, лицензию, crypto или пароль БД.
Ему доступны только ключ подписи и пароль Redis, root filesystem read-only,
service account token отключён, capabilities сброшены. В существующем CRD нет
Push resource requests/limits: для ресурсов используйте namespace LimitRange.
Размер tmpfs ограничен; debug logging запросов отключён, временные файлы не архивируются.

PHP overlay управляет секцией `pull` в `.settings.php`, включая SHA-256 HMAC,
protobuf, URL и `guest=N`. При отключении Push прежние настройки внешнего Push
из release не восстанавливаются: `nginx`, `push`, `websocket` становятся `N`.

| Путь | Назначение |
|---|---|
| `/bitrix/subws/` | Подписка WebSocket с Upgrade |
| `/bitrix/sub/` | Long polling, nginx перенаправляет в upstream `/bitrix/subws/` |
| `/bitrix/rest/` | Web publish: protobuf, проверка подписанного канала и public receiver |
| `/bitrix/pub/` | Только доверенный backend publish через внутренний publisher Service:9010 |

nginx не пишет access logs для URL с channel tokens. Upstreams используют dynamic DNS
и shared zones; startup читает resolver и первый search suffix из Pod resolv.conf
(`ClusterFirst`), поэтому cluster domain не зашит как `cluster.local`. Эти два runtime
DNS tokens заменяет shell после проверки допустимых символов; Secret values там нет.
HTTP/HTTPS и WS/WSS пути задаются с `#DOMAIN#`; Gateway/TLS ещё не реализованы.

## Граница доверия

Upstream `/bitrix/pub/` в этом режиме **не проверяет подпись**: это API доверенного
приложения. nginx всегда возвращает 403 для `/bitrix/pub/` и `/server-stat/`, включая
варианты регистра. Publisher Service не публикуется наружу. NetworkPolicy разрешает
publisher ingress только web/cron Pods этого BitrixCluster в том же namespace,
subscriber — только его web Pods. Политика создаётся перед Deployment.

Нужен CNI, который применяет NetworkPolicy. Сам факт создания ресурса не доказывает
изоляцию; дополнительные allow-политики складываются с этой политикой. Пользователи,
которые могут создавать Pods с соответствующими labels, входят в границу доверия.
Не создавайте внешний Service/Route для publisher. Egress политикой не ограничивается.

Push server использует Redis DB 0 и глобальные `pushserver:*` keys без настраиваемого
namespace/prefix. `redis.cache.sid` изолирует cache, но **не Push**. Для независимых
BitrixCluster нужны отдельные Redis endpoints/instances; общая DB 0 допустима только
внутри одной установки и её границы доверия. ACL Redis должен разрешать требуемые
команды и Pub/Sub. Redis outage снимает readiness, TCP liveness не рестартует живой
Node процесс только из-за недоступности Redis.

## Изменения и отключение

Ротация Secret меняет revision annotations и запускает rollout web, cron и Push.
Ротация ключа не атомарна: старые и новые Pods временно сосуществуют; действующие
подписи могут отклоняться. Планируйте окно обслуживания и переподключение клиентов.
Upstream Node завершает процесс по сигналу без гарантии drain WebSocket: клиент
должен reconnect. Exactly-once доставка и сохранение каждого события при рестартах
этим оператором не гарантируются.

`enabled=false` сначала удаляет owned Deployments с foreground propagation,
сохраняет ограничения ingress до удаления Pods, затем удаляет owned Services/Policies
и обновляет web/PHP конфиги. Cleanup выполняется до проверки Secrets, поэтому
отсутствующий ключ не мешает отключению. Чужие ресурсы не принимаются и не удаляются.
Versioned ConfigMaps пока остаются до удаления CR (pruning — отдельная задача).

## Проверки

- Envtest: идемпотентность, независимые replicas, stale generation, ротация ключа,
  invalid Secret, cleanup без ключа, foreground ordering и ResourceCollision.
- Native images: PHP settings/сложные Secret values; настоящий upstream Push,
  Redis authentication, fanout на два subscriber, reconnect, long polling,
  HMAC отказ и подписанный REST/protobuf.
- Изолированный kind: nginx WebSocket/polling/REST, блокировка public publish,
  scale 1/2 → 2/3, Secret rotation и отключение ресурсов.

Default kindnet **не обеспечивает NetworkPolicy enforcement**. Проверяются сами
объекты/селекторы; подтверждение сетевой изоляции на выбранном production CNI остаётся
отдельной инфраструктурной проверкой. Тесты не включают лицензированное ядро Bitrix:
совместимость конкретной версии модуля pull и её поведение требуют smoke test.

Ориентиры: [env-docker snapshot](https://github.com/bitrix-tools/env-docker/tree/46aff98f3e3d76a7f83fc50317dc3f6502a80865),
`sources/bxpush33`, `confs/nginx/inc/push.conf`;
[Bitrix settings](https://docs.1c-bitrix.ru/pages/framework/settings.html).
Файлы написаны самостоятельно под Kubernetes; upstream server/image используются
как runtime dependency. Provenance — в [templates guide](../internal/runtime/templates/README.md).
