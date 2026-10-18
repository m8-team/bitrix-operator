package controller

import (
	"context"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Keep each component's reconcile method callable directly by tests and by
// future dedicated controllers without changing the global orchestration.
func (r *BitrixClusterReconciler) reconcileRuntimeConfig(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	for _, configuration := range workload.ConfigMaps(c, in.bundle) {
		if err := r.apply(ctx, c, configuration); err != nil {
			return err
		}
	}
	return nil
}

func (r *BitrixClusterReconciler) reconcileRuntimePush(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	if !pushEnabled(c) {
		return nil
	}
	return r.reconcilePush(ctx, c, in.bundle, in.revision)
}

func (r *BitrixClusterReconciler) reconcileRuntimeWeb(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	for _, obj := range []any{workload.Service(c), workload.DisruptionBudget(c), workload.Deployment(c, in.bundle, in.revision, in.image)} {
		if err := r.apply(ctx, c, obj); err != nil {
			return err
		}
	}
	return nil
}

func (r *BitrixClusterReconciler) reconcileRuntimeCron(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	if !c.Spec.Runtime.Cron.Enabled {
		return nil
	}
	return r.reconcileCron(ctx, c, in.bundle, in.revision, in.image, in.pvcBound)
}

func (r *BitrixClusterReconciler) observeRuntimeWeb(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	deployment := deploymentObject()
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, deployment); err != nil {
		return err
	}
	if err := r.reconcileHPA(ctx, c); err != nil {
		return err
	}
	desired := webDesiredReplicas(c, deployment)
	rollout := observeDeployment(deployment, desired)
	c.Status.Runtime.Web = rollout.status
	if desired > 0 && rollout.ready && in.pvcBound {
		condition(c, "WebReady", metav1.ConditionTrue, "RolloutComplete", "The desired web replicas are ready and uploads storage is bound")
		c.Status.Release = &bx.ReleaseStatus{Repository: c.Spec.Release.Image.Repository, Tag: c.Spec.Release.Image.Tag, Digest: c.Spec.Release.Image.Digest}
	}
	if rollout.deadlineExceeded {
		return block(progressDeadlineExceeded, "The web Deployment exceeded its rollout deadline")
	}
	in.webReady = rollout.ready
	return nil
}

func (r *BitrixClusterReconciler) reconcileRuntimeSites(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	return r.reconcileSites(ctx, c, in.bundle, in.revision, in.image)
}

func (r *BitrixClusterReconciler) reconcileRuntimeConfigGC(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	if in.webReady && in.pvcBound {
		return r.pruneConfigMaps(ctx, c, in.bundle)
	}
	return nil
}
