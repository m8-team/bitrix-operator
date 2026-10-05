# Web autoscaling (итерация 7.1)

Оператор создаёт один `autoscaling/v2` HorizontalPodAutoscaler `<cluster>-web`,
который масштабирует Deployment с nginx и PHP. Используется существующий
`runtime.web.autoscaling`; API и CRD BitrixCluster не изменены.
Cron и Push этим HPA не масштабируются.

## Включение

Нужны работающий resource metrics API (обычно metrics-server), доступные ресурсы узлов
и RWX uploads при `maxReplicas > 1`. Установку metrics-server выполняет инфраструктура.
Пример [autoscaling-patch.yaml](../config/examples/autoscaling-patch.yaml) — merge patch
для уже подготовленного CR с RWX; namespace укажите через `kubectl -n`.

```sh
kubectl patch bitrixcluster example --type=merge --patch-file=config/examples/autoscaling-patch.yaml
kubectl get hpa example-web
kubectl get bitrixcluster example -o yaml
```

- `minReplicas >= 1`, `maxReplicas >= minReplicas`.
- Задайте хотя бы один из `targetCPUUtilization` и `targetMemoryUtilization` (1–100%).
  Неявного CPU target нет. Если указаны оба, Kubernetes выбирает большую рекомендацию.
- Для каждого выбранного ресурса положительные `requests` обязательны у **nginx и PHP**:
  используются Resource metrics всего Pod, не только PHP. Limits не заменяют requests
  при проверке CR. Учитывайте дополнительные контейнеры, если их внедряет admission.
- При возможности масштабирования выше одной реплики нужен `ReadWriteMany`, даже если
  `replicas` или `minReplicas` равны 1. Изменить accessModes существующего uploads PVC
  нельзя: подготовьте RWX и перенесите данные отдельно.
- Значения requests в примере иллюстративные; подберите их по нагрузке и PHP workers.

Не создавайте второй HPA и не запускайте другой контроллер replicas для того же web
Deployment. Коллизия имени с чужим HPA приводит к `ResourceCollision`; оператор его
не присваивает и не удаляет.

## Передача replicas и отключение

При создании нового web Deployment `runtime.web.replicas` задаёт начальное число Pod,
ограниченное диапазоном min/max. При включении HPA у существующего Deployment текущее
число Pod сохраняется; в диапазон его приводит HPA.

Чтобы удаление `spec.replicas` из SSA-конфигурации не сбросило масштаб до 1, временный
field manager `bitrix-operator-replica-handoff` сначала разделяет владение текущим
значением. Основной manager `bitrix-operator` затем исключает поле из Apply. Первый
scale update HPA забирает владение у временного manager. Пока update не было,
его запись может оставаться: повторных записей replicas оператор не делает.
Это соответствует [передаче владения SSA](https://kubernetes.io/docs/reference/using-api/server-side-apply/#transferring-ownership).
Перезапуск оператора, обновление release/config/Secrets и изменение `web.replicas`
не сбрасывают масштаб работающего HPA.

При `enabled: false` или удалении `autoscaling` оператор сначала удаляет собственный HPA
и ждёт исчезновения объекта (включая finalizers), затем возвращает управление полем
replicas и применяет `runtime.web.replicas`. Удаление HPA выполняется даже при ошибке
в другой части runtime-конфигурации; сам Deployment обновляется после её исправления.
При изменении включённой конфигурации на невалидную старые ресурсы остаются на месте:
смотрите причину `Ready=False`, исправьте CR или явно отключите HPA.

## Статусы и ограничения

`runtime.web.desiredReplicas`, `WebReady` и проверка образов всех web Pod используют
фактический `Deployment.spec.replicas` при включённом HPA. При scale-out/scale-in Ready
ожидает завершения текущего rollout и проверки site IDs. Pod readiness/liveness не
зависят от доступности metrics-server.

`AutoscalingReady=True` означает, что HPA сообщает `ScalingActive=True` и `AbleToScale=True`.
`AutoscalingReady` входит в общий Ready. При отсутствии metrics API web продолжает
работать, но `AutoscalingReady=False / MetricsPending` и общий `Ready=False`.
`ScalingLimited=True` на достигнутой границе не считается ошибкой.

У HPA conditions нет собственной observedGeneration. Если controller заполняет
`status.observedGeneration`, устаревшее значение отклоняется; если поле отсутствует,
оператор использует последние conditions. Они не доказывают, что метрика была собрана
после последнего изменения spec. Диагностика metrics/HPA доступна через `kubectl describe hpa`.

Используется стандартное поведение Kubernetes HPA, включая окно стабилизации уменьшения
масштаба. Behavior, custom/external metrics, масштабирование до нуля и VPA в этот этап
не входят. Ручное scale до 0 не считается готовым web и может деактивировать HPA;
оператор не переписывает replicas поверх внешнего решения.

## Проверки

Envtest: SSA handoff без сброса, scale subresource, идемпотентность и повторное создание
reconciler, смена метрик, начальный диапазон, ожидание удаления HPA, чужое владение,
resource requests, RWX/RWOP и проверка сайтов при масштабе, отличном от CR replicas.

Kind E2E: metrics-server **v0.9.0** (asset закреплён SHA-256), отсутствие metrics,
CPU-нагрузка в synthetic PHP fixture, реальный scale-out, restart manager, уменьшение
maxReplicas и отключение HPA с восстановлением фиксированного масштаба.
Тест использует одноузловой RWX hostPath и не подтверждает работу распределённого CSI.
`--kubelet-insecure-tls` применяется только к metrics-server внутри disposable kind;
для production настройте проверяемые сертификаты kubelet.
