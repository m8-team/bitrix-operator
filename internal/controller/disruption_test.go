package controller

import (
	"github.com/m8-team/bitrix-operator/internal/workload"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Web disruption budget", func() {
	It("repairs drift, preserves metadata, heals deletion and follows HPA scale without changing the budget", func() {
		c := reconcileWeb(webSample())
		key := client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}
		p := &policyv1.PodDisruptionBudget{}
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		Expect(p.OwnerReferences[0].UID).To(Equal(c.UID))
		Expect(p.Spec.Selector.MatchLabels).To(Equal(webDeployment(c).Spec.Selector.MatchLabels))
		version := p.ResourceVersion
		reconcileWeb(c)
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		Expect(p.ResourceVersion).To(Equal(version))
		p.Spec.MaxUnavailable = new(intstr.FromInt32(0))
		p.Annotations = map[string]string{"example.com/note": "keep"}
		Expect(k8sClient.Update(ctx, p)).To(Succeed())
		reconcileWeb(c)
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		Expect(*p.Spec.MaxUnavailable).To(Equal(intstr.FromInt32(1)))
		Expect(*p.Spec.UnhealthyPodEvictionPolicy).To(Equal(policyv1.AlwaysAllow))
		Expect(p.Annotations).To(HaveKeyWithValue("example.com/note", "keep"))
		oldUID := p.UID
		Expect(k8sClient.Delete(ctx, p)).To(Succeed())
		reconcileWeb(c)
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		Expect(p.UID).NotTo(Equal(oldUID))
		enableAutoscaling(c)
		reconcileWeb(c)
		scaleWeb(c, 4)
		reconcileWeb(c)
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		Expect(*p.Spec.MaxUnavailable).To(Equal(intstr.FromInt32(1)))
		Expect(*webDeployment(c).Spec.Replicas).To(Equal(int32(4)))
	})
	It("rejects a foreign budget without adopting or modifying it", func() {
		c := webSample()
		r := &BitrixClusterReconciler{Client: k8sClient}
		foreign := workload.DisruptionBudget(c)
		foreign.OwnerReferences = nil
		Expect(r.apply(ctx, c, foreign)).To(HaveOccurred())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("ResourceCollision"))
		p := &policyv1.PodDisruptionBudget{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, p)).To(Succeed())
		Expect(p.OwnerReferences).To(BeEmpty())
	})
})
