package controller

import (
	"encoding/json"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func enableAutoscaling(c *bx.BitrixCluster) {
	c.Spec.Runtime.Web.Autoscaling = &bx.AutoscalingSpec{Enabled: true, MinReplicas: 2, MaxReplicas: 5, TargetCPUUtilization: new(int32(70))}
	c.Spec.Runtime.Web.Nginx.Resources.Requests = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi")}
	c.Spec.Runtime.Web.PHP.Resources.Requests = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("64Mi")}
	Expect(k8sClient.Update(ctx, c)).To(Succeed())
}
func webHPA(c *bx.BitrixCluster) *autoscalingv2.HorizontalPodAutoscaler {
	h := &autoscalingv2.HorizontalPodAutoscaler{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, h)).To(Succeed())
	return h
}
func scaleWeb(c *bx.BitrixCluster, n int32) {
	d := webDeployment(c)
	scale := &autoscalingv1.Scale{}
	Expect(k8sClient.SubResource("scale").Get(ctx, d, scale)).To(Succeed())
	scale.Spec.Replicas = n
	Expect(k8sClient.SubResource("scale").Update(ctx, d, &client.SubResourceUpdateOptions{SubResourceBody: scale, UpdateOptions: client.UpdateOptions{FieldManager: "test-autoscaler"}})).To(Succeed())
}
func managerOwnsReplicas(c *bx.BitrixCluster, manager string) bool {
	for _, f := range webDeployment(c).ManagedFields {
		if f.Manager != manager || f.FieldsV1 == nil {
			continue
		}
		var fields map[string]any
		Expect(json.Unmarshal(f.FieldsV1.GetRawBytes(), &fields)).To(Succeed())
		_, found, err := unstructured.NestedFieldNoCopy(fields, "f:spec", "f:replicas")
		Expect(err).NotTo(HaveOccurred())
		if found {
			return true
		}
	}
	return false
}
func markHPAActive(c *bx.BitrixCluster) {
	h := webHPA(c)
	h.Status.ObservedGeneration = new(h.Generation)
	h.Status.Conditions = []autoscalingv2.HorizontalPodAutoscalerCondition{{Type: autoscalingv2.AbleToScale, Status: corev1.ConditionTrue, Reason: "Ready"}, {Type: autoscalingv2.ScalingActive, Status: corev1.ConditionTrue, Reason: "ValidMetricFound"}}
	Expect(k8sClient.Status().Update(ctx, h)).To(Succeed())
}

var _ = Describe("Web autoscaling", func() {
	It("hands off current replicas without defaulting or fighting scale updates", func() {
		c := webSample()
		c.Spec.Runtime.Web.Replicas = 3
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(managerOwnsReplicas(c, "bitrix-operator")).To(BeTrue())
		enableAutoscaling(c)
		reconcileWeb(c)
		Expect(*webDeployment(c).Spec.Replicas).To(Equal(int32(3)))
		Expect(managerOwnsReplicas(c, "bitrix-operator")).To(BeFalse())
		Expect(managerOwnsReplicas(c, replicaHandoffManager)).To(BeTrue())
		h := webHPA(c)
		Expect(h.Spec.ScaleTargetRef.Name).To(Equal(workload.Name(c, "web")))
		Expect(h.Spec.Metrics[0].Resource.Name).To(Equal(corev1.ResourceCPU))
		scaleWeb(c, 4)
		Expect(managerOwnsReplicas(c, replicaHandoffManager)).To(BeFalse())
		reconcileWeb(c)
		Expect(*webDeployment(c).Spec.Replicas).To(Equal(int32(4)))
		Expect(c.Status.Runtime.Web.DesiredReplicas).To(Equal(int32(4)))
		version := webDeployment(c).ResourceVersion
		reconcileWeb(c) // A fresh reconciler per invocation also exercises restart behavior.
		Expect(webDeployment(c).ResourceVersion).To(Equal(version))
		c.Spec.Runtime.Web.Replicas = 2
		c.Spec.Runtime.Web.Autoscaling.TargetCPUUtilization = nil
		c.Spec.Runtime.Web.Autoscaling.TargetMemoryUtilization = new(int32(80))
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(*webDeployment(c).Spec.Replicas).To(Equal(int32(4)))
		Expect(webHPA(c).Spec.Metrics).To(HaveLen(1))
		Expect(webHPA(c).Spec.Metrics[0].Resource.Name).To(Equal(corev1.ResourceMemory))
	})
	It("clamps the initial scale and waits for HPA removal before restoring fixed replicas", func() {
		c := webSample()
		enableAutoscaling(c)
		reconcileWeb(c)
		Expect(*webDeployment(c).Spec.Replicas).To(Equal(int32(2)))
		scaleWeb(c, 4)
		h := webHPA(c)
		h.Finalizers = []string{"tests.example.com/hold"}
		Expect(k8sClient.Update(ctx, h)).To(Succeed())
		c.Spec.Runtime.Web.Autoscaling.Enabled = false
		c.Spec.Runtime.Web.Replicas = 3
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("AutoscalingStopping"))
		reconcileWeb(c)
		Expect(*webDeployment(c).Spec.Replicas).To(Equal(int32(4)))
		h = webHPA(c)
		h.Finalizers = nil
		Expect(k8sClient.Update(ctx, h)).To(Succeed())
		reconcileWeb(c)
		Expect(*webDeployment(c).Spec.Replicas).To(Equal(int32(3)))
		Expect(managerOwnsReplicas(c, "bitrix-operator")).To(BeTrue())
		Expect(meta.FindStatusCondition(c.Status.Conditions, "AutoscalingReady").Reason).To(Equal("Disabled"))
	})
	It("validates resource requests, explicit metrics and future multi-pod storage", func() {
		c := webSample()
		enableAutoscaling(c)
		c.Spec.Runtime.Web.Nginx.Resources.Requests = nil
		Expect(validateWorkload(c).Error()).To(ContainSubstring("cpu request for nginx"))
		c.Spec.Runtime.Web.Autoscaling.TargetCPUUtilization = nil
		Expect(validateWorkload(c).Error()).To(ContainSubstring("At least one"))
		c.Spec.Runtime.Web.Autoscaling.TargetMemoryUtilization = new(int32(80))
		Expect(validateWorkload(c).Error()).To(ContainSubstring("memory request for nginx"))
		c.Spec.Runtime.Web.Nginx.Resources.Requests = corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("64Mi")}
		c.Spec.Storage.Uploads.PersistentVolumeClaim.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		Expect(validateWorkload(c).Error()).To(ContainSubstring("ReadWriteMany"))
		c.Spec.Storage.Uploads.PersistentVolumeClaim.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOncePod}
		Expect(validateWorkload(c)).To(HaveOccurred())
		c.Spec.Runtime.Web.Autoscaling.MinReplicas = 1
		c.Spec.Runtime.Web.Autoscaling.MaxReplicas = 1
		Expect(validateWorkload(c)).To(Succeed())
	})
	It("uses the actual scale for web/site readiness and reports unavailable metrics", func() {
		c := webSample()
		enableAutoscaling(c)
		reconcileWeb(c)
		scaleWeb(c, 3)
		markWebReady(c)
		reconcileWeb(c)
		finishValidation(c, validationJob(c), sitesValidated, []string{"s1"})
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady")).To(BeTrue())
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "SitesValidated")).To(BeTrue())
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeFalse())
		markHPAActive(c)
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeTrue())
		h := webHPA(c)
		h.Status.ObservedGeneration = new(h.Generation - 1)
		Expect(k8sClient.Status().Update(ctx, h)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "AutoscalingReady")).To(BeFalse())
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady")).To(BeTrue())
	})
	It("does not adopt or delete a foreign HPA", func() {
		c := webSample()
		enableAutoscaling(c)
		h := workload.HPA(c)
		h.OwnerReferences = nil
		Expect(k8sClient.Create(ctx, h)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("ResourceCollision"))
		c.Spec.Runtime.Web.Autoscaling = nil
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(webHPA(c).OwnerReferences).To(BeEmpty())
	})
})
