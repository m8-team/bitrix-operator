package controller

import (
	"context"
	"encoding/json"
	"fmt"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const replicaHandoffManager = "bitrix-operator-replica-handoff"

func validateAutoscaling(c *bx.BitrixCluster) error {
	if !workload.AutoscalingEnabled(c) {
		return nil
	}
	a := c.Spec.Runtime.Web.Autoscaling
	if a.MinReplicas < 1 || a.MaxReplicas < a.MinReplicas {
		return block("InvalidAutoscaling", "Autoscaling requires 1 <= minReplicas <= maxReplicas")
	}
	if a.TargetCPUUtilization == nil && a.TargetMemoryUtilization == nil {
		return block("InvalidAutoscaling", "At least one CPU or memory utilization target is required")
	}
	for _, metric := range []struct {
		name   corev1.ResourceName
		target *int32
	}{{corev1.ResourceCPU, a.TargetCPUUtilization}, {corev1.ResourceMemory, a.TargetMemoryUtilization}} {
		if metric.target == nil {
			continue
		}
		if *metric.target < 1 || *metric.target > 100 {
			return block("InvalidAutoscaling", "Utilization targets must be between 1 and 100 percent")
		}
		for _, container := range []struct {
			name      string
			resources corev1.ResourceRequirements
		}{{"nginx", c.Spec.Runtime.Web.Nginx.Resources}, {phpContainer, c.Spec.Runtime.Web.PHP.Resources}} {
			request := container.resources.Requests[metric.name]
			if request.Sign() <= 0 {
				return block("MissingResourceRequests", fmt.Sprintf("Autoscaling requires a positive %s request for %s", metric.name, container.name))
			}
		}
	}
	return nil
}

// HPA must be fully gone before returning replicas to the operator, including when
// unrelated runtime configuration is invalid. Deletion uses UID/RV preconditions.
func (r *BitrixClusterReconciler) deleteHPA(ctx context.Context, c *bx.BitrixCluster) error {
	h := &autoscalingv2.HorizontalPodAutoscaler{}
	err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, h)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !controlledByCluster(c, h) {
		return block("ResourceCollision", "The HPA is not controlled by this BitrixCluster")
	}
	if h.DeletionTimestamp.IsZero() {
		if err = client.IgnoreNotFound(r.Delete(ctx, h, client.Preconditions{UID: &h.UID, ResourceVersion: &h.ResourceVersion})); err != nil {
			return err
		}
	}
	return block("AutoscalingStopping", "Waiting for the owned HPA to disappear before restoring fixed replicas")
}

func (r *BitrixClusterReconciler) reconcileHPA(ctx context.Context, c *bx.BitrixCluster) error {
	if !workload.AutoscalingEnabled(c) {
		return nil
	}
	if err := r.apply(ctx, c, workload.HPA(c)); err != nil {
		return err
	}
	h := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, h); err != nil {
		return err
	}
	// HPA conditions do not have observedGeneration; honor the top-level value when
	// provided. Kubernetes implementations may omit it, so this is observed health,
	// not proof that a particular metric sample was collected after a spec edit.
	if h.Status.ObservedGeneration != nil && *h.Status.ObservedGeneration < h.Generation {
		condition(c, "AutoscalingReady", metav1.ConditionFalse, "AutoscalingPending", "Waiting for the HPA controller to observe the current configuration")
		return nil
	}
	active, able := false, false
	for _, cond := range h.Status.Conditions {
		if cond.Type == autoscalingv2.ScalingActive {
			active = cond.Status == corev1.ConditionTrue
		}
		if cond.Type == autoscalingv2.AbleToScale {
			able = cond.Status == corev1.ConditionTrue
		}
	}
	if active && able {
		condition(c, "AutoscalingReady", metav1.ConditionTrue, "AutoscalingActive", "The HPA reports available metrics and a scalable target")
	} else {
		condition(c, "AutoscalingReady", metav1.ConditionFalse, "MetricsPending", "Waiting for the HPA to report ScalingActive and AbleToScale; check metrics-server and resource requests")
	}
	return nil
}

func webDesiredReplicas(c *bx.BitrixCluster, d *appsv1.Deployment) int32 {
	if workload.AutoscalingEnabled(c) && d.Spec.Replicas != nil {
		return *d.Spec.Replicas
	}
	return c.Spec.Runtime.Web.Replicas
}

// Transfer a shared replica field to a temporary SSA manager before omission by
// the regular operator manager. This avoids defaulting to 1. The HPA's first scale
// update takes ownership away from the temporary manager. Never repeat after the
// regular manager has relinquished the field, including after operator restarts.
func (r *BitrixClusterReconciler) handoffReplicas(ctx context.Context, live *unstructured.Unstructured) error {
	owns := false
	for _, field := range live.GetManagedFields() {
		if field.Manager != "bitrix-operator" || field.Subresource != "" || field.FieldsV1 == nil {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(field.FieldsV1.GetRawBytes(), &fields); err != nil {
			return err
		}
		if _, found, err := unstructured.NestedFieldNoCopy(fields, "f:spec", "f:replicas"); err != nil {
			return err
		} else if found {
			owns = true
		}
	}
	if !owns {
		return nil
	}
	replicas, found, err := unstructured.NestedInt64(live.Object, "spec", "replicas")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("deployment replicas missing during handoff")
	}
	handoff := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": deploymentKind,
		"metadata": map[string]any{"name": live.GetName(), "namespace": live.GetNamespace(), "resourceVersion": live.GetResourceVersion()},
		"spec":     map[string]any{"replicas": replicas},
	}}
	if err = r.Apply(ctx, client.ApplyConfigurationFromUnstructured(handoff), client.FieldOwner(replicaHandoffManager)); err != nil {
		return err
	}
	uid := live.GetUID()
	if err = r.reader().Get(ctx, client.ObjectKeyFromObject(live), live); err != nil {
		return err
	}
	if live.GetUID() != uid {
		return block("ResourceCollision", "The Deployment was replaced during replica ownership handoff")
	}
	return nil
}
