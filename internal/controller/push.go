package controller

import (
	"context"
	"errors"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const pushPublisher = "publisher"
const pushSubscriber = "subscriber"

func pushEnabled(c *bx.BitrixCluster) bool {
	return c.Spec.Runtime.Push != nil && c.Spec.Runtime.Push.Enabled
}
func initializePushStatus(c *bx.BitrixCluster) {
	condition(c, "PushReady", metav1.ConditionTrue, "Disabled", "Push workloads are disabled")
	if pushEnabled(c) {
		c.Status.Runtime.PushPublisher.DesiredReplicas = c.Spec.Runtime.Push.Publisher.Replicas
		c.Status.Runtime.PushSubscriber.DesiredReplicas = c.Spec.Runtime.Push.Subscriber.Replicas
		condition(c, "PushReady", metav1.ConditionUnknown, "NotObserved", "Push Deployments have not yet been observed for this generation")
	}
}

func (r *BitrixClusterReconciler) reconcilePush(ctx context.Context, c *bx.BitrixCluster, b *render.Bundle, revision string) (ComponentResult, error) {
	result := ComponentResult{}
	if err := r.resourceManager().Apply(ctx, c, workload.PushConfig(c, b)); err != nil {
		return result, err
	}
	ready := true
	for _, role := range []string{pushPublisher, pushSubscriber} {
		// Apply NetworkPolicy and Service before launching the trusted Push backend.
		for _, obj := range []any{workload.PushPolicy(c, role), workload.PushService(c, role), workload.PushDeployment(c, b, revision, role)} {
			if err := r.resourceManager().Apply(ctx, c, obj); err != nil {
				return result, err
			}
		}
		d := &appsv1.Deployment{}
		if err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "push-"+role)}, d); err != nil {
			return result, err
		}
		desired := c.Spec.Runtime.Push.Subscriber.Replicas
		if role == pushPublisher {
			desired = c.Spec.Runtime.Push.Publisher.Replicas
		}
		rollout := observeDeployment(d, desired)
		// Partial observations survive a later role's Apply/Get error.
		if role == pushPublisher {
			result.RuntimeFor(c.Status.Runtime).PushPublisher = rollout.status
		} else {
			result.RuntimeFor(c.Status.Runtime).PushSubscriber = rollout.status
		}
		ready = ready && rollout.ready
		if rollout.deadlineExceeded {
			return result, block(progressDeadlineExceeded, "A Push Deployment exceeded its rollout deadline")
		}
	}
	if ready {
		result.SetCondition(c.Generation, conditionPushReady, metav1.ConditionTrue, "RolloutComplete", "Both desired Push workloads are ready")
	} else {
		result.SetCondition(c.Generation, conditionPushReady, metav1.ConditionFalse, "RolloutPending", "Waiting for both current Push Deployment rollouts")
	}
	return result, nil
}

// Cleanup precedes Secret/config validation and preserves ingress restrictions until Pods terminate.
func (r *BitrixClusterReconciler) deletePush(ctx context.Context, c *bx.BitrixCluster, result *ComponentResult) error {
	pending := false
	for _, role := range []string{pushPublisher, pushSubscriber} {
		d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: workload.Name(c, "push-"+role), Namespace: c.Namespace}}
		found, err := r.deletePushObject(ctx, c, d, true, result)
		if err != nil {
			return err
		}
		pending = pending || found
	}
	if pending {
		return waitFor("PushStopping", "Waiting for owned Push Deployments and their Pods to terminate", pendingRequeueAfter)
	}
	for _, role := range []string{pushPublisher, pushSubscriber} {
		m := metav1.ObjectMeta{Name: workload.Name(c, "push-"+role), Namespace: c.Namespace}
		for _, obj := range []client.Object{&corev1.Service{ObjectMeta: m}, &networkingv1.NetworkPolicy{ObjectMeta: m}} {
			if _, err := r.deletePushObject(ctx, c, obj, false, result); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *BitrixClusterReconciler) deletePushObject(ctx context.Context, c *bx.BitrixCluster, obj client.Object, foreground bool, result *ComponentResult) (bool, error) {
	found, err := r.resourceManager().DeleteOwned(ctx, c, obj, foreground)
	if err != nil {
		if collision, ok := errors.AsType[*blocked](err); ok {
			result.SetCondition(c.Generation, conditionPushReady, metav1.ConditionFalse, collision.reason, collision.message)
		} else {
			result.SetCondition(c.Generation, conditionPushReady, metav1.ConditionFalse, "CleanupFailed", "Could not inspect or delete an owned Push resource")
		}
	}
	return found, err
}
