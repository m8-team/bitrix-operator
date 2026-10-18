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
		condition(c, "PushReady", metav1.ConditionFalse, "RolloutPending", "Waiting for the desired Push publisher and subscriber rollouts")
	}
}

func (r *BitrixClusterReconciler) reconcilePush(ctx context.Context, c *bx.BitrixCluster, b *render.Bundle, revision string) error {
	if err := r.apply(ctx, c, workload.PushConfig(c, b)); err != nil {
		return err
	}
	ready := true
	for _, role := range []string{pushPublisher, pushSubscriber} {
		// Policies must exist before new Pods are started: /pub is a trusted backend API.
		for _, obj := range []any{workload.PushPolicy(c, role), workload.PushService(c, role), workload.PushDeployment(c, b, revision, role)} {
			if err := r.apply(ctx, c, obj); err != nil {
				return err
			}
		}
		d := &appsv1.Deployment{}
		if err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "push-"+role)}, d); err != nil {
			return err
		}
		desired := c.Spec.Runtime.Push.Subscriber.Replicas
		target := &c.Status.Runtime.PushSubscriber
		if role == pushPublisher {
			desired = c.Spec.Runtime.Push.Publisher.Replicas
			target = &c.Status.Runtime.PushPublisher
		}
		rollout := observeDeployment(d, desired)
		*target = rollout.status
		ready = ready && rollout.ready
		if rollout.deadlineExceeded {
			return block(progressDeadlineExceeded, "A Push Deployment exceeded its rollout deadline")
		}
	}
	if ready {
		condition(c, "PushReady", metav1.ConditionTrue, "RolloutComplete", "Both desired Push workloads are ready")
	}
	return nil
}

// Cleanup precedes Secret/config validation and preserves ingress restrictions until Pods terminate.
func (r *BitrixClusterReconciler) deletePush(ctx context.Context, c *bx.BitrixCluster) error {
	pending := false
	for _, role := range []string{pushPublisher, pushSubscriber} {
		d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: workload.Name(c, "push-"+role), Namespace: c.Namespace}}
		found, err := r.deletePushObject(ctx, c, d, true)
		if err != nil {
			return err
		}
		pending = pending || found
	}
	if pending {
		return block("PushStopping", "Waiting for owned Push Deployments and their Pods to terminate")
	}
	for _, role := range []string{pushPublisher, pushSubscriber} {
		m := metav1.ObjectMeta{Name: workload.Name(c, "push-"+role), Namespace: c.Namespace}
		for _, obj := range []client.Object{&corev1.Service{ObjectMeta: m}, &networkingv1.NetworkPolicy{ObjectMeta: m}} {
			if _, err := r.deletePushObject(ctx, c, obj, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *BitrixClusterReconciler) deletePushObject(ctx context.Context, c *bx.BitrixCluster, obj client.Object, foreground bool) (bool, error) {
	found, err := r.deleteOwnedResource(ctx, c, obj, foreground)
	if err != nil {
		var collision *blocked
		if errors.As(err, &collision) {
			condition(c, "PushReady", metav1.ConditionFalse, collision.reason, collision.message)
		} else {
			condition(c, "PushReady", metav1.ConditionFalse, "CleanupFailed", "Could not inspect or delete an owned Push resource")
		}
	}
	return found, err
}
