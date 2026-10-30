package controller

import (
	"context"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Reconcile orchestrates the application and publication controllers.
// Status transitions and persistence are isolated in status.go.
func (r *BitrixClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cluster := &bx.BitrixCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !cluster.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	status := NewStatusManager(r, cluster)
	status.Begin()
	resourceErr := r.reconcileResources(ctx, status)
	// GC happens after the rollout, so a GC failure must not suppress
	// otherwise valid publication or clear observed Gateway readiness.
	runtimeValid := resourceErr == nil || failedComponent(resourceErr, "config-gc")
	gatewayStage := observedStep{name: stageGateway, run: func(ctx context.Context, c *bx.BitrixCluster, _ *runtimeInputs) (ComponentResult, error) {
		return r.reconcileGateway(ctx, c, runtimeValid)
	}}
	publication, publicationErr := gatewayStage.Reconcile(ctx, cluster, nil)
	status.Apply(publication)
	result, err := status.Resolve(resourceErr, publicationErr)
	if patchErr := status.Commit(ctx); patchErr != nil {
		recordReconcileOutcome(cluster, patchErr)
		return ctrl.Result{}, patchErr
	}
	recordReconcileOutcome(cluster, err)
	return result, err
}
