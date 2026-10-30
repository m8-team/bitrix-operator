# Gateway API и TLS (итерация 6.2)

Оператор создаёт HTTPRoute v1 для сайтов и направляет запросы на web Service:80.
Gateway, GatewayClass, TLS Secrets и ReferenceGrant заранее создаёт инфраструктура.
Оператор их читает, но не изменяет и не копирует закрытые ключи.
BitrixCluster API и CRD остались без изменений.

## Зависимости и включение

Go dependency: Gateway API **v1.6.2**. Нужны обслуживаемые v1 APIs для Gateway,
GatewayClass, HTTPRoute и ReferenceGrant, а также работающий Gateway controller.
Установка оператора не устанавливает Gateway API CRDs. Без них внутренние web/cron/push
продолжают работать; включённая публикация получает `GatewayReady=False / DependencyMissing`.
После установки CRDs перезапустите manager: optional watches регистрируются при запуске.
Удаление Gateway API CRDs при работающем manager не поддерживается.

```sh
kubectl -n bitrix-operator-system rollout restart deployment/bitrix-operator-controller-manager
```

Фрагмент существующего BitrixCluster (остальные обязательные поля сохраните):

```yaml
spec:
  sites:
    - id: s1
      hosts: [portal.example.com]
      tls:
        secretName: portal-tls
  networking:
    gateway:
      enabled: true
      parentRef:
        name: shared
        namespace: edge
        sectionName: https
```

Пример инфраструктурных объектов — [gateway-https.yaml](../config/examples/gateway-https.yaml).
Он предполагает существующие namespaces `edge`, `bitrix`, GatewayClass `shared` и
Secret `bitrix/portal-tls` типа `kubernetes.io/tls`. BitrixCluster находится в `bitrix`.
Подготовьте DNS и сертификат своим способом, например через cert-manager.

Для HTTP выберите listener с `protocol: HTTP` и удалите `sites[].tls`.
TLS у сайта означает намерение использовать HTTPS: HTTP listener с этим полем
отклоняется (`TLSListenerMismatch`). HTTPS требует TLS Secret у каждого сайта.
Один BitrixCluster выбирает один listener; HTTP→HTTPS redirect, TLS passthrough,
автовыпуск сертификатов и backend TLS в эту итерацию не входят.

## Проверки и готовность

- Listener должен разрешать HTTPRoute и namespace приложения через `allowedRoutes`
  (`Same`, `All` или `Selector`), а hostname listener должен покрывать все hosts.
- HTTPS listener завершает TLS и явно ссылается на каждый Secret из `sites[].tls`
  в namespace BitrixCluster. Для ссылки из другого namespace нужен ReferenceGrant
  в namespace Secret: from Gateway namespace, to Secret с подходящим именем.
- Проверяются тип Secret, PEM/key pair, SAN всех hosts, срок действия и serverAuth EKU.
  Публичная цепочка доверия и отзыв сертификата не проверяются; доверие CA проверяет клиент.
- У Gateway нужны актуальные `Accepted` и `Programmed`, у listener также `ResolvedRefs`.
  У каждого HTTPRoute нужны `Accepted` и `ResolvedRefs` с текущей `observedGeneration`
  от controllerName выбранного GatewayClass и для выбранного parent/section.
- Другой BitrixCluster на том же listener или сторонний HTTPRoute с пересекающимися
  hosts блокирует публикацию (`HostnameConflict`). Проверка консервативная: учитывает
  заявленные маршруты, даже если другой controller пока их не принял.

`GatewayReady` входит в общий `Ready`. `status.sites[].urls` появляются после готовности
Gateway, web и успешной проверки site IDs. Нестандартный listener port входит в URL.
URL не подтверждает настройку DNS или доступность внешней сети.
Отсутствующий Secret, несовместимый listener, конфликт hosts или отозванный ReferenceGrant
снимают готовность и удаляют собственные Routes. Удаление site и отключение publication
также удаляют лишние Routes. Чужие объекты оператор не присваивает.
Ожидание статуса Gateway/Route само по себе не удаляет корректный желаемый Route.

События Gateway, HTTPRoute, ReferenceGrant, GatewayClass, namespace labels и TLS Secret
запускают сверку. Дополнительная периодическая сверка каждые 30 секунд обнаруживает
истечение срока сертификата без изменения Secret. Это асинхронная сверка, а не гарантия
мгновенного прекращения обслуживания на границе срока.

## Контроль HTTPS после переключения маршрутов

При замене HTTPRoute оператор удерживает старый маршрут с пересекающимся
hostname до актуальных условий `Accepted=True` / `ResolvedRefs=True` у всех
новых маршрутов и `Accepted=True` / `Programmed=True` у Gateway и listener.
Это проверка **control plane**, а не фактической доставки HTTP-запроса.

Важное ограничение Gateway API: при равнозначных hostname, path и остальных
условиях выбор между HTTPRoutes обычно отдаёт приоритет **более старому**
маршруту. Поэтому положительный результат GET во время сосуществования двух
маршрутов может проходить через *старый* HTTPRoute и не доказывает
работоспособность нового. Для проверки завершённой замены требуются оба
свидетельства:

1. Старый принадлежащий оператору HTTPRoute удалён, остался только ожидаемый
   маршрут с новым именем, и его статус актуален.
2. HTTPS-запрос на фиксированный hostname через *настоящий* Envoy Gateway,
   с проверкой TLS сертификата, возвращает ожидаемый `SITE_ID` из PHP
   приложения.

Сценарий `test/e2e/web/gateway-handoff.sh` в изолированном kind-кластере
проверяет замену `s1 → s2 → s1`, идентичность маршрутов, HTTPS и фактический
ответ PHP через Gateway. Пробник запускается **из CI**, не из процесса
оператора. Он использует фиксированный локальный port-forward и имя
тестового сайта, без доступа к production URL или пользовательским Secrets.

Это проверка **eventual reachability**, а не непрерывности каждого запроса
во время переключения. Настоящая гарантия zero-downtime потребует
отдельного stage/cutover механизма с явной идентичностью ответа новой
конфигурации, проверкой backend endpoints, сетевыми ограничениями пробника
и контролируемым временем ожидания. Не добавляйте в manager произвольный
HTTP client по `sites[].hosts`: это расширяет поверхность SSRF, создаёт
зависимость от DNS, proxy и egress, а также не различает старый и новый
маршрут при одинаковом URL.

## TLS termination и ротация

Родной nginx template передаёт PHP `HTTPS=on`, `REQUEST_SCHEME=https` и listener port
для опубликованных HTTPS-сайтов. Эти значения определяются конфигурацией оператора;
клиентские `X-Forwarded-Proto` / `X-Forwarded-Port` не определяют FastCGI transport metadata.
Прямой запрос к внутреннему Service для такого сайта получает ту же metadata.
Ограничьте доступ к backend сетью своей инфраструктуры; этот режим предполагает
TLS termination на доверенном Gateway, а не TLS внутри web Pod.

Ротацию сертификата выполняет владелец Secret. Gateway controller перечитывает сертификат,
оператор повторяет проверки. TLS Secret не входит в runtime Secret revision:
обновление только сертификата не перезапускает web/cron/push. Смена listener port или
режима публикации изменяет native nginx config и требует обычного rollout.
Ключ Bitrix Crypto и ключ подписи Push — отдельные зависимости с другой ротацией.

## Проверки

Envtest проверяет актуальность статусов, отсутствие APIs, listener/hostname, TLS,
ReferenceGrant, конфликты hosts, разбиение более 16 hosts на Routes, удаление и idempotency.
Kind E2E использует **Envoy Gateway v1.9.2**, закреплённый install asset с SHA-256:
HTTP, HTTPS с проверкой сертификата клиентом, cross-namespace permissions, отзыв grant,
ротация TLS без rollout приложения и установка CRDs после старта manager.
Это проверка конкретной реализации; совместимость остальных Gateway controllers
и настоящий Bitrix release требуют отдельных испытаний.
