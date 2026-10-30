package controller

import (
	"context"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Keep each component's reconcile method callable directly by tests and by
// future dedicated controllers without changing the global orchestration.
func (r *BitrixClusterReconciler) reconcileRuntimeConfig(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	for _, configuration := range workload.ConfigMaps(c, in.desired.bundle) {
		if err := r.resourceManager().Apply(ctx, c, configuration); err != nil {
			return err
		}
	}
	return nil
}

func (r *BitrixClusterReconciler) reconcileRuntimePush(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) (ComponentResult, error) {
	if !pushEnabled(c) {
		return ComponentResult{}, nil
	}
	return r.reconcilePush(ctx, c, in.desired.bundle, in.desired.revision)
}

func (r *BitrixClusterReconciler) reconcileRuntimeWeb(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	for _, obj := range []any{workload.Service(c), workload.DisruptionBudget(c), workload.Deployment(c, in.desired.bundle, in.desired.revision, in.desired.image)} {
		if err := r.resourceManager().Apply(ctx, c, obj); err != nil {
			return err
		}
	}
	return nil
}

func (r *BitrixClusterReconciler) reconcileRuntimeCron(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) (ComponentResult, error) {
	if !c.Spec.Runtime.Cron.Enabled {
		return ComponentResult{}, nil
	}
	return r.reconcileCron(ctx, c, in.desired.bundle, in.desired.revision, in.desired.image, in.observed.storageBound)
}

func (r *BitrixClusterReconciler) observeRuntimeWeb(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) (ComponentResult, error) {
	result := ComponentResult{}
	deployment := deploymentObject()
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, deployment); err != nil {
		result.SetCondition(c.Generation, conditionWebReady, metav1.ConditionUnknown, "ObservationFailed", "Could not read the web Deployment")
		return result, err
	}
	desired := webDesiredReplicas(c, deployment)
	rollout := observeDeployment(deployment, desired)
	result.RuntimeFor(c.Status.Runtime).Web = rollout.status
	if desired > 0 && rollout.ready && in.observed.storageBound {
		result.SetCondition(c.Generation, conditionWebReady, metav1.ConditionTrue, "RolloutComplete", "The desired web replicas are ready and uploads storage is bound")
		result.Release = &bx.ReleaseStatus{Repository: c.Spec.Release.Image.Repository, Tag: c.Spec.Release.Image.Tag, Digest: c.Spec.Release.Image.Digest}
		result.ReleaseChanged = true
	} else {
		result.SetCondition(c.Generation, conditionWebReady, metav1.ConditionFalse, "RolloutPending", "Waiting for the current web Deployment and uploads storage")
	}
	// Retain observed web rollout even when HPA reconciliation fails.
	hpaResult, err := r.reconcileHPA(ctx, c)
	result.Conditions = append(result.Conditions, hpaResult.Conditions...)
	if err != nil {
		return result, err
	}
	if rollout.deadlineExceeded {
		return result, block(progressDeadlineExceeded, "The web Deployment exceeded its rollout deadline")
	}
	return result, nil
}

func (r *BitrixClusterReconciler) reconcileRuntimeSites(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) (ComponentResult, error) {
	return r.reconcileSites(ctx, c, in.desired.bundle, in.desired.revision, in.desired.image)
}

func (r *BitrixClusterReconciler) reconcileRuntimeConfigGC(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) error {
	if in.observed.storageBound && meta.IsStatusConditionTrue(c.Status.Conditions, conditionWebReady) {
		return r.pruneConfigMaps(ctx, c, in.desired.bundle)
	}
	return nil
}
