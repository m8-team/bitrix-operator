package controller

import (
	"context"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *BitrixClusterReconciler) reconcileResources(ctx context.Context, c *bx.BitrixCluster) error {
	if err := r.deleteDisabledRuntime(ctx, c); err != nil {
		return err
	}
	if err := validateWorkload(c); err != nil {
		return err
	}
	b, err := render.RenderWithPort(c.Spec, r.publicationPort(ctx, c), workload.PushEndpoints(c))
	if err != nil {
		return block("InvalidRuntimeConfiguration", err.Error())
	}
	image, err := render.ReleaseReference(c.Spec.Release.Image)
	if err != nil {
		return block("InvalidRelease", err.Error())
	}
	revision, err := r.secrets(ctx, c, b)
	if err != nil {
		return err
	}
	pvc, err := r.storage(ctx, c)
	if err != nil {
		return err
	}
	condition(c, "StorageReady", metav1.ConditionFalse, "VolumePending", "Waiting for the uploads PVC to bind")
	if pvc.Status.Phase == corev1.ClaimBound {
		condition(c, "StorageReady", metav1.ConditionTrue, "VolumeBound", "Uploads PVC is bound")
	}
	for _, configuration := range workload.ConfigMaps(c, b) {
		if err = r.apply(ctx, c, configuration); err != nil {
			return err
		}
	}
	if pushEnabled(c) {
		if err = r.reconcilePush(ctx, c, b, revision); err != nil {
			return err
		}
	}
	if err = r.apply(ctx, c, workload.Service(c)); err != nil {
		return err
	}
	if err = r.apply(ctx, c, workload.DisruptionBudget(c)); err != nil {
		return err
	}
	if err = r.apply(ctx, c, workload.Deployment(c, b, revision, image)); err != nil {
		return err
	}
	if c.Spec.Runtime.Cron.Enabled {
		if err = r.reconcileCron(ctx, c, b, revision, image, pvc.Status.Phase == corev1.ClaimBound); err != nil {
			return err
		}
	}
	deployment := deploymentObject()
	if err = r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, deployment); err != nil {
		return err
	}
	if err = r.reconcileHPA(ctx, c); err != nil {
		return err
	}
	desired := webDesiredReplicas(c, deployment)
	rollout := observeDeployment(deployment, desired)
	c.Status.Runtime.Web = rollout.status
	if desired > 0 && rollout.ready && pvc.Status.Phase == corev1.ClaimBound {
		condition(c, "WebReady", metav1.ConditionTrue, "RolloutComplete", "The desired web replicas are ready and uploads storage is bound")
		c.Status.Release = &bx.ReleaseStatus{Repository: c.Spec.Release.Image.Repository, Tag: c.Spec.Release.Image.Tag, Digest: c.Spec.Release.Image.Digest}
	}
	if rollout.deadlineExceeded {
		return block(progressDeadlineExceeded, "The web Deployment exceeded its rollout deadline")
	}
	if err := r.reconcileSites(ctx, c, b, revision, image); err != nil {
		return err
	}
	if rollout.ready && pvc.Status.Phase == corev1.ClaimBound {
		return r.pruneConfigMaps(ctx, c, b)
	}
	return nil
}

func (r *BitrixClusterReconciler) deleteDisabledRuntime(ctx context.Context, c *bx.BitrixCluster) error {
	if !workload.AutoscalingEnabled(c) {
		if err := r.deleteHPA(ctx, c); err != nil {
			return err
		}
	}
	if !c.Spec.Runtime.Cron.Enabled {
		if err := r.deleteCron(ctx, c); err != nil {
			return err
		}
	}
	if !pushEnabled(c) {
		if err := r.deletePush(ctx, c); err != nil {
			return err
		}
	}
	return nil
}
