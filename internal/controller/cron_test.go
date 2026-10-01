package controller

import (
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func cronSample() *bx.BitrixCluster {
	c := webSample()
	c.Spec.Runtime.Cron.Enabled = true
	c.Spec.Runtime.Cron.Agents = &bx.AgentsSpec{Enabled: true, Interval: metav1.Duration{Duration: 1500 * time.Millisecond}}
	Expect(k8sClient.Update(ctx, c)).To(Succeed())
	return c
}
func cronDeployment(c *bx.BitrixCluster) *appsv1.Deployment {
	d := &appsv1.Deployment{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "cron")}, d)).To(Succeed())
	return d
}

var _ = Describe("Cron reconciliation", func() {
	It("converges across reconciler restarts, repairs singleton drift and rolls on interval/release/agent changes", func() {
		c := reconcileWeb(cronSample())
		d := cronDeployment(c)
		Expect(*d.Spec.Replicas).To(Equal(int32(1)))
		Expect(d.Spec.Strategy.Type).To(Equal(appsv1.RecreateDeploymentStrategyType))
		Expect(d.Spec.Template.Labels["app.kubernetes.io/name"]).NotTo(Equal(webDeployment(c).Spec.Selector.MatchLabels["app.kubernetes.io/name"]))
		version := d.ResourceVersion
		reconcileWeb(c) // helper constructs a fresh reconciler every time
		Expect(cronDeployment(c).ResourceVersion).To(Equal(version))
		three := int32(3)
		d.Spec.Replicas = &three
		Expect(k8sClient.Update(ctx, d)).To(Succeed())
		reconcileWeb(c)
		Expect(*cronDeployment(c).Spec.Replicas).To(Equal(int32(1)))
		oldHash := d.Spec.Template.Annotations[workload.ConfigHash]
		c.Spec.Runtime.Cron.Agents.Interval.Duration = 3 * time.Second
		c.Spec.Runtime.Cron.Agents.Enabled = false
		c.Spec.Release.Image.Tag = "next"
		c.Spec.Release.Image.Digest = ""
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		d = cronDeployment(c)
		Expect(d.Spec.Template.Annotations[workload.ConfigHash]).NotTo(Equal(oldHash))
		Expect(d.Spec.Template.Spec.InitContainers[0].Image).To(HaveSuffix(":next"))
		Expect(meta.IsStatusConditionFalse(c.Status.Conditions, "CronReady")).To(BeTrue())
	})

	It("requires RWX and validates interval without changing the CRD", func() {
		c := cronSample()
		c.Spec.Storage.Uploads.PersistentVolumeClaim.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("InvalidStorageMode"))
		c.Spec.Storage.Uploads.PersistentVolumeClaim.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}
		c.Spec.Runtime.Cron.Agents.Interval.Duration = 0
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("InvalidRuntimeConfiguration"))
	})

	It("reports only the observed cron generation as ready", func() {
		c := reconcileWeb(cronSample())
		p := uploads(c)
		p.Status.Phase = corev1.ClaimBound
		Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
		d := cronDeployment(c)
		d.Status = appsv1.DeploymentStatus{Replicas: 1, ReadyReplicas: 1, AvailableReplicas: 1, UpdatedReplicas: 1}
		Expect(k8sClient.Status().Update(ctx, d)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionFalse(c.Status.Conditions, "CronReady")).To(BeTrue())
		d.Status.ObservedGeneration = d.Generation
		Expect(k8sClient.Status().Update(ctx, d)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "CronReady")).To(BeTrue())
		Expect(c.Status.Runtime.Cron.ReadyReplicas).To(Equal(int32(1)))
		Expect(meta.IsStatusConditionFalse(c.Status.Conditions, "Ready")).To(BeTrue())
	})

	It("deletes owned cron before dependency checks and waits for foreground deletion", func() {
		c := reconcileWeb(cronSample())
		webHash := webDeployment(c).Spec.Template.Annotations[workload.ConfigHash]
		c.Spec.Runtime.Cron.Enabled = false
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: c.Spec.License.SecretRef.Name, Namespace: c.Namespace}}
		Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
		reconcileWeb(c)
		d := cronDeployment(c)
		Expect(d.DeletionTimestamp.IsZero()).To(BeFalse())
		Expect(meta.FindStatusCondition(c.Status.Conditions, "CronReady").Reason).To(Equal("CronStopping"))
		Expect(webDeployment(c).Spec.Template.Annotations[workload.ConfigHash]).To(Equal(webHash))
		// Envtest has no garbage collector; simulate its finalizer completion after checking the policy.
		Expect(d.Finalizers).To(ContainElement("foregroundDeletion"))
		d.Finalizers = nil
		Expect(k8sClient.Update(ctx, d)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("SecretMissing"))
		Expect(meta.FindStatusCondition(c.Status.Conditions, "CronReady").Reason).To(Equal("Disabled"))
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(d), &appsv1.Deployment{}))).To(BeTrue())
		Expect(uploads(c).DeletionTimestamp.IsZero()).To(BeTrue())
	})

	It("never adopts or deletes a foreign cron Deployment", func() {
		c := reconcileWeb(cronSample())
		d := cronDeployment(c)
		d.OwnerReferences = nil
		Expect(k8sClient.Update(ctx, d)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("ResourceCollision"))
		c.Spec.Runtime.Cron.Enabled = false
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("ResourceCollision"))
		Expect(cronDeployment(c).DeletionTimestamp.IsZero()).To(BeTrue())
	})
})
