# Controller architecture

The Bitrix operator follows a one-way reconciliation pipeline. The Kubernetes
manager handles watches and queues; the controller coordinates changes; narrow
components apply resources; and the status layer reports what was actually
observed. The runtime order is a safety contract, not an optimization detail.

```text
Kubernetes events / indexes
  -> BitrixClusterReconciler.Reconcile          internal/controller/reconcile.go
     -> initializeReconcileStatus               internal/controller/status.go
     -> reconcileResources                     internal/controller/runtime_reconcile.go
        -> prepareRuntime                      dependencies, validation, storage
        -> runtimeComponents                   seven explicit steps
           -> ConfigMaps                      runtime_components.go
           -> Push (policy before Pods)        push.go
           -> Web Service/PDB/Deployment       runtime_components.go
           -> Cron                             cron.go
           -> Web rollout and HPA              autoscaling.go, deployment.go
           -> Site validation                  sites.go
           -> versioned ConfigMap GC           configmap_cleanup.go
     -> reconcileGateway                      gateway.go
     -> apply ComponentResults + resolve policy  status.go
     -> persist status + transition events      status.go
```

## Responsibilities

- **Orchestration** (`reconcile.go`, `runtime_reconcile.go`): sequencing,
  dependency preparation, cancellation and stopping on the first component
  error. A component returns an error; the stage runner does not silently
  continue with inconsistent prerequisites.
- **Status** (`status.go`): creates fresh desired-generation Conditions,
  classifies component/publication errors, preserves transition timestamps and
  persists status with optimistic locking. The error-policy functions need no
  API server and have direct unit tests.
- **Components** (`runtime_components.go`, `cron.go`, `push.go`,
  `sites.go`, `autoscaling.go`): narrow functions that act on a prepared
  immutable runtime bundle plus reconcile-local observation state. Each is
  independently callable. The site validation Job is identity-bound; it must
  not be accepted merely because a cached status says Ready.
- **Resource helpers** (`internal/kubeops`): Kubernetes mechanics that do
  not import controller packages, such as guarded deletion and owner UID/kind/
  API-version verification. Controller wrappers map these errors to domain
  Conditions. Always pass the uncached reader to ownership-sensitive deletion
  and read-after-write checks.
- **Workload builders** (`internal/workload`) remain pure declarative
  Kubernetes configuration builders. **Rendering** (`internal/runtime`)
  remains independent of the Kubernetes client.

## Testing

- Standard Go unit tests exercise stage order, failure short-circuit,
  cancellation, status Conditions and `kubeops` with a fake client; no
  envtest binary, running cluster or secrets are needed.
- Controller envtest integration tests verify SSA, ownerReferences,
  finalizers, rollout status and BitrixCluster CRDs against real Kubernetes
  API servers.
- The kind/Helm/Cilium/recovery CI workflows validate deployment ordering,
  network enforcement and user-facing behavior. Run the whole matrix before
  merging or deploying changes to stage boundaries.

## Extending the operator

Add a method implementing `runtimeComponent`, then place it in
`runtimeComponents()` *after* all prerequisites and *before* dependent
components. Update the order contract test, add a direct unit test and an
envtest or e2e scenario for resources that the step owns. Do not add arbitrary
API reads, status patches or route pruning inside the stage runner.

The top-level `BitrixClusterReconciler` retains one Kubernetes client and
watches; avoid introducing another manager, controller, generic template
framework or global mutable state merely to add a runtime component.

## P1: component observations and error policy

Every runtime stage implements `runtimeComponent.Reconcile(...)` and returns a
`ComponentResult` plus an error. The ConfigMaps, Web apply and ConfigMap GC
stages return results directly and reject any accidental shared status mutation.
Statusful legacy stages still use `captureComponentResult` on an isolated
BitrixCluster snapshot. The shared `StatusManager` applies only reported
Condition, runtime, release and site deltas, including observations made before
an error. Neither kind of stage writes the status subresource directly.

`StatusReady` (the `Ready` Condition) and `Phase` represent overall health;
component Conditions represent observed component health. A failure attributed
to `config-gc` does not reset a confirmed `WebReady=True` or
`SitesValidated=True`. Similarly, Gateway failures do not reset observed
internal runtime readiness. If the GC stage fails *after* completing the runtime
pipeline, Gateway still reconciles (subject to its own dependency checks).

Reconcile errors have three dispositions:

| Disposition | Examples | Queue behavior | Phase |
|---|---|---|---|
| Waiting | In-progress rollout, SSA conflict | `RequeueAfter` or dependency watch | Progressing |
| Blocked | Missing Secret, invalid configuration | `RequeueAfter` awaiting user changes | Degraded |
| Transient | API timeout, unavailable dependency, permission error | Return error; controller-runtime rate limiter | Degraded |

Do not return a transient error *and* rely on `RequeueAfter`: controller-runtime
ignores that result when an error is non-nil. The error policy is centralized in
`reconcile_errors.go` and the final status aggregation in `status.go`.

A component's Conditions are evidence, not a substitute for `Ready`; global
health may be degraded while a previously observed Web Deployment remains
ready. Regression tests cover GC failure, site-validation failure, Gateway
failure, cancellation, snapshot isolation, and retry dispositions.

## P2: desired, applied, observed and periodic reconciliation

The reconciliation state has three distinct meanings:

- **Desired** is the rendered runtime specification (`runtimeDesiredState`):
  immutable bundle, image reference and Secret revision prepared once.
- **Applied** is each `ComponentResult.Applied` stage outcome: a successful
  declarative reconcile or no-op. It must not be interpreted as a successful
  Deployment rollout, running PHP process or validated site.
- **Observed** consists of the PVC binding snapshot (`runtimeObservedState`)
  plus rollout counters, actual image digests and Conditions captured in
  `ComponentResult` and aggregated into the CR status.

The inputs are never used as a pipeline scratchpad. In particular,
`runtimeInputs.webReady` has been removed; ConfigMap GC relies on the
`WebReady=True` Condition actually recorded by the Web observation stage
together with bound storage. Stage ordering therefore follows explicit
observed status rather than an implicit mutable boolean.

**Periodic resync**: Pending clusters retain a 30-second safety poll, user
configuration blocks retain their existing retry policy, and transient
Kubernetes API errors use the workqueue's rate limiter. Fully Ready clusters
resync at 10 minutes plus a deterministic 0–119 second UID-based spread.
Watches on BitrixCluster, owned workloads, Secrets, storage and Gateway
dependencies still trigger immediate reconciliation; periodic resync is a
bounded safety net for missed changes, not a replacement for watches.

Partial failures and restarts are not resumable transactions. Reconcile
always regenerates desired state from the CR and the actual Kubernetes
objects. Successful prior applies are idempotent, and a retry may resume
after fixing a foreign-object collision or recreating a required dependency,
without re-creating retained PVCs or adopting unrelated objects.

## ClickHouse-inspired reconciliation primitives

Three explicitly named primitives now organize lifecycle operations without changing
the `BitrixCluster` API or relaxing the existing safety invariants:

- **ReconcileStep** (`step.go`) executes named stages in order. `StepBlocked`
  skips dependent stages and permits independent `Always` observations;
  unexpected errors still short-circuit. `runRuntimeComponents` adapts the
  seven established runtime stages to this runner, retaining isolated
  `ComponentResult` deltas, stage metrics, and partial observations.
- **StatusManager** (`statusmanager.go`) owns the reconcile snapshot,
  readiness policy, optimistic status patch, and post-persistence Events.
  Stage code still creates `ComponentResult` observations, not direct status
  subresource mutations.
- **ResourceManager** (`resourcemanager.go`) owns Kubernetes SSA and retained
  PVC mutation. It deliberately keeps owner UID checks, create-before-apply,
  managedFields migration, HPA replica handoff, and resourceVersion guards
  rather than copying ClickHouse's spec-hash/Update strategy.

Component implementations call ResourceManager directly for SSA, PVC and
UID-guarded deletion. The new step API supports future independent observations
without making the current reconciliation graph concurrent.

## Incremental adapter removal

StatusManager is passed through the entire runtime pipeline and is the only
path used to aggregate stage observations. ResourceManager is called directly
for Kubernetes apply, retained PVC storage and UID-guarded deletion. The old
`r.apply`, `r.storage`, `r.deleteOwnedResource` and
`applyComponentResult` wrappers are removed.

The ConfigMaps, Web resource apply and ConfigMap GC stages now return
`ComponentResult` directly through `applyOnlyStep`, preserving stage
metrics and failing if they accidentally mutate the shared status. More
complex stages (Push, Cron, Web observation, Sites, Gateway and preparation)
continue to use the isolated `captureComponentResult` bridge until their
Condition/runtime/site observations can be migrated individually with
dedicated regression tests. This avoids weakening evidence-based readiness
during the transition.
