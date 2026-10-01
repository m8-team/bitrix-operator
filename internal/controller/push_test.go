package controller

import (
	"strings"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func pushSample() *bx.BitrixCluster {
	c := webSample()
	c.Spec.Runtime.Push = &bx.PushRuntimeSpec{Enabled: true, Image: "push:test", SecretRef: corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "push-key"}, Key: "key"}, Publisher: bx.ReplicaSpec{Replicas: 1}, Subscriber: bx.ReplicaSpec{Replicas: 2}}
	Expect(k8sClient.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "push-key", Namespace: c.Namespace}, Data: map[string][]byte{"key": []byte(strings.Repeat("k", 64))}})).To(Succeed())
	Expect(k8sClient.Update(ctx, c)).To(Succeed())
	return c
}
func pushDeployment(c *bx.BitrixCluster, role string) *appsv1.Deployment {
	d := &appsv1.Deployment{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "push-"+role)}, d)).To(Succeed())
	return d
}

var _ = Describe("Push reconciliation", func() {
	It("converges across restarts, scales independently and requires the current generation", func() {
		c := reconcileWeb(pushSample())
		for _, role := range []string{"publisher", "subscriber"} {
			d := pushDeployment(c, role)
			version := d.ResourceVersion
			reconcileWeb(c)
			Expect(pushDeployment(c, role).ResourceVersion).To(Equal(version))
			n := *d.Spec.Replicas
			d.Status = appsv1.DeploymentStatus{Replicas: n, ReadyReplicas: n, UpdatedReplicas: n, AvailableReplicas: n}
			Expect(k8sClient.Status().Update(ctx, d)).To(Succeed())
		}
		reconcileWeb(c)
		Expect(meta.IsStatusConditionFalse(c.Status.Conditions, "PushReady")).To(BeTrue())
		for _, role := range []string{"publisher", "subscriber"} {
			d := pushDeployment(c, role)
			d.Status.ObservedGeneration = d.Generation
			Expect(k8sClient.Status().Update(ctx, d)).To(Succeed())
		}
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "PushReady")).To(BeTrue())
		c.Spec.Runtime.Push.Subscriber.Replicas = 3
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(*pushDeployment(c, "subscriber").Spec.Replicas).To(Equal(int32(3)))
		Expect(*pushDeployment(c, "publisher").Spec.Replicas).To(Equal(int32(1)))
		Expect(c.Status.Runtime.PushSubscriber.DesiredReplicas).To(Equal(int32(3)))
		Expect(meta.IsStatusConditionFalse(c.Status.Conditions, "PushReady")).To(BeTrue())
	})
	It("rotates web and both Push workloads and rejects invalid keys without exposing values", func() {
		c := reconcileWeb(pushSample())
		old := pushDeployment(c, "publisher").Spec.Template.Annotations[workload.SecretRevision]
		s := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: "push-key"}, s)).To(Succeed())
		s.Data["key"] = []byte(strings.Repeat("r", 64))
		Expect(k8sClient.Update(ctx, s)).To(Succeed())
		reconcileWeb(c)
		current := webDeployment(c).Spec.Template.Annotations[workload.SecretRevision]
		Expect(current).NotTo(Equal(old))
		for _, role := range []string{"publisher", "subscriber"} {
			Expect(pushDeployment(c, role).Spec.Template.Annotations[workload.SecretRevision]).To(Equal(current))
		}
		s.Data["key"] = []byte("invalid-secret-key")
		Expect(k8sClient.Update(ctx, s)).To(Succeed())
		reconcileWeb(c)
		condition := meta.FindStatusCondition(c.Status.Conditions, "PushReady")
		Expect(condition.Reason).To(Equal("SecretKeyInvalid"))
		Expect(condition.Message).NotTo(ContainSubstring("invalid-secret-key"))
		Expect(pushDeployment(c, "publisher").Spec.Template.Annotations[workload.SecretRevision]).To(Equal(current))
	})
	It("keeps ingress restrictions until foreground deletion completes, even when the Secret is missing", func() {
		c := reconcileWeb(pushSample())
		c.Spec.Runtime.Push.Enabled = false
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "push-key", Namespace: c.Namespace}})).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "PushReady").Reason).To(Equal("PushStopping"))
		for _, role := range []string{"publisher", "subscriber"} {
			d := pushDeployment(c, role)
			Expect(d.DeletionTimestamp.IsZero()).To(BeFalse())
			Expect(d.Finalizers).To(ContainElement("foregroundDeletion"))
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(d), &networkingv1.NetworkPolicy{})).To(Succeed())
			// Envtest has no garbage collector; complete deletion after checking the foreground policy.
			d.Finalizers = nil
			Expect(k8sClient.Update(ctx, d)).To(Succeed())
		}
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "PushReady").Reason).To(Equal("Disabled"))
		for _, role := range []string{"publisher", "subscriber"} {
			key := client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "push-"+role)}
			for _, obj := range []client.Object{&appsv1.Deployment{}, &corev1.Service{}, &networkingv1.NetworkPolicy{}} {
				Expect(apierrors.IsNotFound(k8sClient.Get(ctx, key, obj))).To(BeTrue())
			}
		}
		Expect(uploads(c).DeletionTimestamp.IsZero()).To(BeTrue())
	})
	It("never adopts or deletes a foreign Push resource", func() {
		c := reconcileWeb(pushSample())
		d := pushDeployment(c, "publisher")
		d.OwnerReferences = nil
		Expect(k8sClient.Update(ctx, d)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("ResourceCollision"))
		c.Spec.Runtime.Push.Enabled = false
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("ResourceCollision"))
		Expect(pushDeployment(c, "publisher").DeletionTimestamp.IsZero()).To(BeTrue())
	})
})
