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
     -> resolveReconcileResult / persist       status.go
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
