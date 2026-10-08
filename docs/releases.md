# Release process

Итерация 8.2 добавляет процесс сборки и публикации **manager image**. Образы PHP/nginx
и пользовательский Bitrix release image имеют отдельный lifecycle. `BitrixCluster`
и native runtime templates этим процессом не изменяются.

## Проверка каждого PR

Workflow `Release images` выполняется на PR, push в main и weekly schedule с `contents: read`:

1. Проверяет release policy, затем собирает Linux amd64 и arm64 на native runners.
2. Использует закреплённые multi-platform digests Go/distroless из Dockerfile,
   `GOTOOLCHAIN=local`, `go.sum`, `go mod verify`, `-mod=readonly`, `-trimpath`.
3. Проверяет архитектуру и UID 65532, запускает manager `--help` без сети,
   с read-only filesystem, без capabilities. Это smoke бинарника; reconcile/E2E
   покрываются существующими отдельными workflows.
4. Trivy 0.75.0 создаёт SPDX JSON inventory, проверяет OS и Go packages. Любая
   HIGH/CRITICAL уязвимость, включая ещё не исправленную, блокирует передачу образа
   в publish job. Ошибка скачивания vulnerability DB также блокирует работу.
   Ignore-файлы не применяются. Scan reports сохраняются и при отказе gate.
5. Сохраняет успешно проверенный image tar и метаданные в immutable Actions artifact.
   Publish job использует только artifacts текущего run, без повторной сборки.

Первый PR scan обнаружил HIGH CVE-2026-84304 и CVE-2026-84445 в транзитивном
`google.golang.org/grpc v1.82.1`. Зависимость обновлена до `v1.83.2` вместе с
требуемыми transitive modules; gate не ослаблен и исключения не добавлены.
[Advisory 1](https://github.com/grpc/grpc-go/security/advisories/GHSA-vp52-pcj8-j9qc),
[advisory 2](https://github.com/grpc/grpc-go/security/advisories/GHSA-2v4p-qf9q-27wj).
Наличие уязвимой версии в inventory само по себе не доказывает достижимость
конкретного server attack path в операторе; обновление устраняет эту зависимость.

Отчёт Trivy отражает базу уязвимостей на момент запуска, а не бессрочную гарантию.
SBOM не включает runtime images сайтов, внешние DB/Redis и зависимости самого Bitrix.
Обновление базовых образов и инструментов проходит через PR с этими проверками.

## Подготовка репозитория к первой публикации

Администратор настраивает environment `release`: required reviewers и разрешённые
release tags. Само наличие `environment: release` в YAML **не создаёт protection
rules**. Ruleset для `v*` должен ограничить создание/изменение/удаление тегов
release maintainers; main защищается обязательными CI checks и review.

GHCR package `ghcr.io/m8-team/bitrix-operator` должен разрешать Actions этого
репозитория запись. Visibility пакета выбирается администратором отдельно;
workflow не делает приватный пакет публичным. Для private package потребителям
нужны registry credentials. Постоянный signing key и PAT для CI не требуются.
Keyless Sigstore публикует certificate identity и подпись в публичном transparency
log; identity содержит repository/workflow/tag, даже для private repository.

## Запуск релиза

После merge и успешного завершения всех main workflows maintainer создаёт новый
тег `vMAJOR.MINOR.PATCH` или `vMAJOR.MINOR.PATCH-rc.N` на **текущем main HEAD**.
Например, первый тестовый release может быть `v0.1.0-rc.1`; документация не означает,
что этот тег уже существует или опубликован.

Release policy требует для точного SHA успешный последний main push run каждого
workflow: Tests, Lint, Runtime, Web, Network, Observability, Recovery,
Install artifacts и Release images. PR results другого SHA, старый успешный run
перед новым failed run, skipped/cancelled/in-progress не принимаются.
Создание тега до завершения main CI даст отказ; после успешного main CI можно
повторить failed tag run. Уже существующий GitHub release не перезаписывается.

После native build/scan и environment gate публикация:

- Повторно проверяет eligibility и резервирует **draft** GitHub release.
- Загружает проверенные tar, сверяет config digest, source SHA и платформу
  с build provenance; публикует временные `build-<run>-<attempt>-<arch>` теги.
- Подписывает platform manifests и прикладывает подписанные SPDX/SLSA v1 predicates.
- Собирает index ровно из `linux/amd64` и `linux/arm64`, подписывает его digest.
- Проверяет подписи/attestations с точными OIDC issuer и workflow identity/tag.
- Генерирует `install.yaml` с immutable image digest, отдельный CRD YAML,
  versioned Helm `.tgz`, SBOM/provenance/scan reports и `image.txt`/`index.json`.
- Подписывает `SHA256SUMS`, проверяет подпись и создаёт version image tag.
  `latest` и плавающие minor tags не используются.
- Загружает assets и заполняет draft release. Maintainer проверяет результат
  и отдельно публикует draft. Образы в GHCR к этому моменту уже загружены.

Helm — дополнительный способ установки самого оператора. Для установки YAML
достаточно kubectl. Release chart сохраняет обязательный `manager.image.reference`:
передайте digest из `image.txt`; chart не разворачивает Bitrix вместо оператора.

Если процесс прервался после reservation, draft остаётся с пометкой незавершённой
сборки. Автоматическая попытка не заменит его или уже существующий version image.
Предпочтительно выяснить причину и выпустить новый RC/version. Очистка временных
тегов и незавершённого draft — отдельное действие maintainers, workflow не удаляет
registry artifacts. Успешные версии/теги никогда не переиспользуются.

## Проверка потребителем

Подставьте реально опубликованный tag и digest из release. Для private GHCR
предварительно выполните registry login. Нужен Cosign 3.1.3 или совместимый.

```bash
VERSION=v0.1.0-rc.1
IMAGE=$(cat image.txt)
IDENTITY="https://github.com/m8-team/bitrix-operator/.github/workflows/release.yml@refs/tags/$VERSION"
cosign verify "$IMAGE" \
  --certificate-identity "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify-blob SHA256SUMS --bundle SHA256SUMS.sigstore.json \
  --certificate-identity "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --check SHA256SUMS
```

Проверяйте digest, ожидаемый repository и **точный** tag. Не заменяйте identity
неограниченным regexp и не отключайте transparency/certificate verification.
Подпись index связывает оба platform digest; SBOM и provenance прикреплены к
соответствующим child manifests, перечисленным в подписанном index:

```bash
# PLATFORM_DIGEST берётся из проверенного index для нужной архитектуры.
cosign verify-attestation "ghcr.io/m8-team/bitrix-operator@$PLATFORM_DIGEST" \
  --type spdxjson --certificate-identity "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify-attestation "ghcr.io/m8-team/bitrix-operator@$PLATFORM_DIGEST" \
  --type slsaprovenance1 --certificate-identity "$IDENTITY" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Provenance имеет SLSA v1 формат, содержит source SHA, platform, base digests,
config digest и workflow run. Predicate формирует наш workflow; **сертифицированный
уровень SLSA и bit-for-bit reproducibility всей OCI упаковки не заявляются**.
BuildKit attestations отключены при Docker tar export; они не выдаются за
сохранившиеся после `docker save/load`.

## Границы и проверка процесса

`make test-release` проверяет отказ для malformed tags, чужого SHA/branch/event,
пропущенных/неуспешных checks и failed reruns. PR CI реально собирает обе платформы
и сканирует их, но **не получает OIDC/packages write и не выполняет публикацию**.
Полный OIDC → GHCR → signed release path необходимо подтвердить первым отдельно
авторизованным RC release. Этот шаг остаётся открытым до фактического запуска.

Установка/upgrade/rollback/uninstall проверяются отдельным kind workflow.
Upgrade CRD и data migrations требуют отдельного review; manager rollback не
откатывает БД. Production CSI recovery и licensed Bitrix smoke остаются отдельными
этапами, см. [план](implementation-plan.md) и [установку](installation.md).

Источники: [Docker image transfer](https://docs.docker.com/build/ci/github-actions/share-image-jobs/),
[Sigstore signing](https://docs.sigstore.dev/cosign/signing/signing_with_containers/),
[verification](https://docs.sigstore.dev/cosign/verifying/verify/),
[Trivy SBOM](https://trivy.dev/docs/latest/guide/supply-chain/sbom/).
