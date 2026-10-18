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
`ComponentResult` plus an error. The current components run through
`captureComponentResult`, which invokes the existing resource logic on a
deep-copied BitrixCluster. Consequently, a stage **cannot mutate the shared
BitrixCluster status**. The orchestrator applies only the reported Condition,
runtime, release, and site deltas after each stage, including observations made
before an error. This is a compatibility adapter for the internal components;
future components can construct results directly without editing the original
CR's status.

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
