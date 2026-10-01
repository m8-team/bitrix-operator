# ADR 0002: web workloads, владение и сохранение uploads

Дата: 2026-09-30. Статус: принято для итерации 3.

## Контекст

Существующий CRD содержит весь необходимый контракт. Требуются повторяемый reconcile,
обновления без конфигурационного drift и сохранение пользовательских данных при удалении CR.
БД и Redis внешние. Готовность транспорта не доказывает существование sites в Bitrix.

## Решение

- Typed SSA builders для web Deployment, Service, versioned immutable ConfigMap;
  owner UID + resourceVersion проверяются до apply. Create предшествует apply новых
  ресурсов, предотвращая присвоение объекта при гонке создания.
- Дополнение 2026-10-01: перед Apply собственная запись managedFields/Update
  переносится в Apply через `client-go/util/csaupgrade.UpgradeManagedFieldsPatch`.
  Patch содержит precondition resourceVersion; чужие field managers не мигрируются.
  Одного ForceOwnership недостаточно: общая запись Create/Update сохраняла поля,
  удалённые из желаемого состояния (например, resource limits).
- ForceOwnership применяется только к явно объявленным управляемым полям уже
  принадлежащего CR объекта. Оно необходимо и при переходе с create/Update field manager
  на Apply, и для исправления ручного drift. Чужие ресурсы не усыновляются.
- Uploads PVC без ownerReference, с UID reservation; отдельный patch для роста,
  запрет shrink/immutable changes. Новый CR с прежним именем требует явного recovery.
- RWO/RWOP ограничены одной replica/Recreate; RWX допускает RollingUpdate.
- Release копируется init container в EmptyDir. Runtime code read-only; writable mounts
  ограничены uploads, temporary, cache и служебным /tmp. Secret volume только у PHP.
- Внешние Secrets наблюдаются metadata-only informer; данные проверяются uncached reader.
  Rollout revision зависит от UID/resourceVersion, не от hash содержимого.
- `WebReady` отражает текущий rollout и dependency readiness; `Ready` остаётся False до
  проверки ядром site IDs. Native PHP probes добавлены как самостоятельные файлы.

## Последствия

Удаление CR не освобождает uploads; необходимы backup и явная процедура удаления/recovery.
RWO rollout имеет простой. Старые ConfigMap расходуют место до удаления CR; pruning отложен.
Изменение даже metadata Secret вызывает rollout. Tag-only release не гарантирует immutable
код между Pod; production должен задавать digest. Site-validation Job остаётся отдельной
итерацией; без него нельзя заявлять полную готовность Bitrix.
