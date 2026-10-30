package controller

import (
	"context"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Disable is independent of current Secrets/storage validity and never deletes a foreign object.
// Foreground deletion keeps web agent settings unchanged until the old workers have terminated.
func (r *BitrixClusterReconciler) deleteCron(ctx context.Context, c *bx.BitrixCluster) error {
	d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: workload.Name(c, "cron"), Namespace: c.Namespace}}
	found, err := r.resourceManager().DeleteOwned(ctx, c, d, true)
	if err != nil {
		return err
	}
	if found {
		return waitFor("CronStopping", "Waiting for the owned cron Deployment and its Pods to terminate", pendingRequeueAfter)
	}
	return nil
}

func (r *BitrixClusterReconciler) reconcileCron(ctx context.Context, c *bx.BitrixCluster, b *render.Bundle, revision, image string, bound bool) (ComponentResult, error) {
	result := ComponentResult{}
	if err := r.resourceManager().Apply(ctx, c, workload.Cron(c, b, revision, image)); err != nil {
		return result, err
	}
	d := &appsv1.Deployment{}
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "cron")}, d); err != nil {
		result.SetCondition(c.Generation, conditionCronReady, metav1.ConditionUnknown, "ObservationFailed", "Could not read the cron Deployment")
		return result, err
	}
	rollout := observeDeployment(d, 1)
	result.RuntimeFor(c.Status.Runtime).Cron = rollout.status
	if rollout.ready && bound {
		result.SetCondition(c.Generation, conditionCronReady, metav1.ConditionTrue, "RolloutComplete", "The desired cron replica is ready and uploads storage is bound")
	} else {
		result.SetCondition(c.Generation, conditionCronReady, metav1.ConditionFalse, "RolloutPending", "Waiting for the current cron Deployment and uploads storage")
	}
	if rollout.deadlineExceeded {
		return result, block(progressDeadlineExceeded, "The cron Deployment exceeded its rollout deadline")
	}
	return result, nil
}
