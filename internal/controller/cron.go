package controller

import (
	"context"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Disable is independent of current Secrets/storage validity and never deletes a foreign object.
// Foreground deletion keeps web agent settings unchanged until the old workers have terminated.
func (r *BitrixClusterReconciler) deleteCron(ctx context.Context, c *bx.BitrixCluster) error {
	d := &appsv1.Deployment{}
	err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "cron")}, d)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	owner := metav1.GetControllerOf(d)
	if owner == nil || owner.UID != c.UID || owner.Kind != "BitrixCluster" || owner.APIVersion != bx.GroupVersion.String() {
		return block("ResourceCollision", "The cron Deployment is not controlled by this BitrixCluster")
	}
	if d.DeletionTimestamp.IsZero() {
		err = r.Delete(ctx, d, client.Preconditions{UID: &d.UID, ResourceVersion: &d.ResourceVersion}, client.PropagationPolicy(metav1.DeletePropagationForeground))
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return block("CronStopping", "Waiting for the owned cron Deployment and its Pods to terminate")
}

func (r *BitrixClusterReconciler) reconcileCron(ctx context.Context, c *bx.BitrixCluster, b *render.Bundle, revision, image string, bound bool) error {
	if err := r.apply(ctx, c, workload.Cron(c, b, revision, image)); err != nil {
		return err
	}
	d := &appsv1.Deployment{}
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "cron")}, d); err != nil {
		return err
	}
	s := d.Status
	c.Status.Runtime.Cron = bx.WorkloadStatus{DesiredReplicas: 1, ReadyReplicas: s.ReadyReplicas, UpdatedReplicas: s.UpdatedReplicas, AvailableReplicas: s.AvailableReplicas}
	if s.ObservedGeneration >= d.Generation && s.Replicas == 1 && s.ReadyReplicas == 1 && s.UpdatedReplicas == 1 && s.AvailableReplicas == 1 && bound {
		condition(c, "CronReady", metav1.ConditionTrue, "RolloutComplete", "The desired cron replica is ready and uploads storage is bound")
	}
	for _, status := range s.Conditions {
		if s.ObservedGeneration >= d.Generation && status.Type == appsv1.DeploymentProgressing && status.Status == corev1.ConditionFalse && status.Reason == "ProgressDeadlineExceeded" {
			return block("ProgressDeadlineExceeded", "The cron Deployment exceeded its rollout deadline")
		}
	}
	return nil
}
