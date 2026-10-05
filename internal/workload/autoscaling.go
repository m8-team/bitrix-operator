package workload

import (
	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func AutoscalingEnabled(c *bx.BitrixCluster) bool {
	return c.Spec.Runtime.Web.Autoscaling != nil && c.Spec.Runtime.Web.Autoscaling.Enabled
}

func HPA(c *bx.BitrixCluster) *autoscalingv2.HorizontalPodAutoscaler {
	a := c.Spec.Runtime.Web.Autoscaling
	h := &autoscalingv2.HorizontalPodAutoscaler{
		TypeMeta:   metav1.TypeMeta{APIVersion: "autoscaling/v2", Kind: "HorizontalPodAutoscaler"},
		ObjectMeta: metav1.ObjectMeta{Name: Name(c, "web"), Namespace: c.Namespace, Labels: labels(c), OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(c, bx.GroupVersion.WithKind("BitrixCluster"))}},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: Name(c, "web")},
			MinReplicas:    &a.MinReplicas, MaxReplicas: a.MaxReplicas,
		},
	}
	for _, metric := range []struct {
		name   corev1.ResourceName
		target *int32
	}{{corev1.ResourceCPU, a.TargetCPUUtilization}, {corev1.ResourceMemory, a.TargetMemoryUtilization}} {
		if metric.target != nil {
			h.Spec.Metrics = append(h.Spec.Metrics, autoscalingv2.MetricSpec{Type: autoscalingv2.ResourceMetricSourceType, Resource: &autoscalingv2.ResourceMetricSource{Name: metric.name, Target: autoscalingv2.MetricTarget{Type: autoscalingv2.UtilizationMetricType, AverageUtilization: metric.target}}})
		}
	}
	return h
}
