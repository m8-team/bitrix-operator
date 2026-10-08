# Обновление зависимостей

`.github/dependabot.yml` включает еженедельные version-update PR для Go modules
(включая indirect), GitHub Actions и Dockerfile images. Проверка назначена на
понедельник 07:00 Europe/Moscow. Kubernetes/controller-runtime и OpenTelemetry
группируются внутри Go ecosystem; остальные изменения идут отдельными PR.
Automerge не включён, major updates не скрываются. Сначала review и CI, затем merge.
Конфигурация начинает действовать после попадания в default branch.

Dependabot security alerts/automatic security updates включаются в настройках
GitHub отдельно; наличие YAML не означает, что они включены. Набор прав/token
обычных PR остаётся read-only; для dependency PR не используется pull_request_target.

Workflow `Release images` дополнительно запускается по понедельникам 06:23 UTC
на main: обе native архитектуры пересобираются, сканируются актуальной Trivy DB,
reports сохраняются в Actions. Schedule никогда не удовлетворяет условию publish
job и не создаёт теги/релизы. HIGH/CRITICAL и ошибки scanner/DB блокируют job.
Проверка не означает сканирование всех старых GHCR images или runtime Bitrix images.

## Что требует согласованного ручного обновления

| Набор | Где закреплён | Обязательные проверки |
|---|---|---|
| Go toolchain и builder image | go.mod, Dockerfile, docs | Совпадение версии, module integrity, native builds, lint/envtest |
| Kubernetes/controller-runtime/controller-gen | go.mod, Makefile, PROJECT/CLI, CI, chart kubeVersion | Обновить через Kubebuilder при необходимости; generate/manifests, обе envtest версии, kind E2E |
| Runtime nginx/PHP/Push | sample, examples, Go defaults, fixture Dockerfiles, contract/golden tests | Согласовать все pins; native syntax, image UID/paths/extensions, web/cron/sites/Push E2E |
| Helm/Kubebuilder/Trivy/actionlint/kind | install scripts, Makefile, workflow | Версия **и checksum** из upstream release; проверить обе поддержанные архитектуры |
| Buildx/BuildKit/Cosign | release workflow inputs | Версии tools не равны SHA Action wrapper; native image build/scan и signed RC rehearsal |
| Envoy/Gateway API/Cilium/metrics-server | dependencies, helper scripts, assets/checksums | Gateway, enforcement и autoscaling scenarios; не переносить kind-only flags в production |
| DB/Redis fixtures | test/e2e/web/dependencies.yaml | Outage/rotation/recovery drill; лицензированный Bitrix smoke отдельно |

Dependabot не обновляет произвольные image strings в Go/YAML, paired checksums
в shell, compatibility claims или лицензионные ограничения Bitrix. Такие изменения
оформляются отдельными PR. Нельзя обновить только wrapper Action и считать,
что инструмент из `with:` тоже обновлён. Сохраняйте полные Action SHA и image digests.

## Review dependency PR

1. Прочитать upstream release notes/advisories и проверить происхождение checksum/digest.
2. Определить связанные pins и совместимость API, runtime paths, UID/GID, Go/Kubernetes.
3. Выполнить нужные проверки: `make lint-fix test verify-generated test-chart test-examples`;
   для новых runtime images — native-tool и соответствующие kind E2E. Генерируемые
   CRD/RBAC/deepcopy не редактировать вручную.
4. Зафиксировать результаты и границы в PR. Если меняется support range, обновить
   [compatibility matrix](compatibility.md), chart kubeVersion и CI совместно.
5. Для уязвимости предпочитать исправленную зависимость; package finding не доказывает
   достижимость attack path, но исключение требует отдельного документированного решения.
   Не снижать severity gate ради зелёного CI.

Не реже раза в месяц maintainer проверяет весь ручной список и открытые пункты
матрицы, оформляет необходимые dependency PR. Это процедура review, а не уже
выполненная автоматическая проверка совместимости upstream releases.

В первом release scan найдены CVE-2026-84304/CVE-2026-84445 и исправлены обновлением
grpc-go до 1.83.2; соответствующий gate сохранён. Подробности в [release guide](releases.md).

Источники: [Dependabot options](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference),
[supported ecosystems](https://docs.github.com/en/code-security/reference/supply-chain-security/supported-ecosystems-and-repositories).
