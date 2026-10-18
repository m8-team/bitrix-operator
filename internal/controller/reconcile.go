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
	before := cluster.DeepCopy()
	initializeReconcileStatus(cluster)
	resourceErr := r.reconcileResources(ctx, cluster)
	publicationErr := r.reconcileGateway(ctx, cluster, resourceErr == nil)
	result, err := resolveReconcileResult(cluster, resourceErr, publicationErr)
	preserveConditionTransitions(before, cluster)
	if patchErr := r.persistReconcileStatus(ctx, before, cluster); patchErr != nil {
		return ctrl.Result{}, patchErr
	}
	return result, err
}
