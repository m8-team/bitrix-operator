package controller

import (
	"context"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

func webSample() *bx.BitrixCluster {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "web-"}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	DeferCleanup(func() { Expect(k8sClient.Delete(ctx, ns)).To(Succeed()) })
	expand := true
	class := &storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{GenerateName: "web-"}, Provisioner: "example.com/test", AllowVolumeExpansion: &expand}
	Expect(k8sClient.Create(ctx, class)).To(Succeed())
	DeferCleanup(func() { Expect(k8sClient.Delete(ctx, class)).To(Succeed()) })
	obj := sample()
	obj.SetNamespace(ns.Name)
	Expect(unstructured.SetNestedField(obj.Object, class.Name, "spec", "storage", "uploads", "persistentVolumeClaim", "storageClassName")).To(Succeed())
	Expect(k8sClient.Create(ctx, obj)).To(Succeed())
	c := &bx.BitrixCluster{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), c)).To(Succeed())
	b, err := render.Render(c.Spec)
	Expect(err).NotTo(HaveOccurred())
	for _, p := range b.Secrets {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: p.Secret.Name, Namespace: ns.Name}, Data: map[string][]byte{}}
		for _, item := range p.Secret.Items {
			secret.Data[item.Key] = []byte("test-credential-only")
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	}
	return c
}

func reconcileWeb(c *bx.BitrixCluster) *bx.BitrixCluster {
	r := &BitrixClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(c), c)).To(Succeed())
	return c
}
func webDeployment(c *bx.BitrixCluster) *appsv1.Deployment {
	d := &appsv1.Deployment{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, d)).To(Succeed())
	return d
}
func uploads(c *bx.BitrixCluster) *corev1.PersistentVolumeClaim {
	p := &corev1.PersistentVolumeClaim{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "uploads")}, p)).To(Succeed())
	return p
}

var _ = Describe("Web and storage reconciliation", func() {
	It("applies idempotently, heals deleted resources and never reports unvalidated sites as ready", func() {
		c := reconcileWeb(webSample())
		d := webDeployment(c)
		p := uploads(c)
		Expect(p.OwnerReferences).To(BeEmpty())
		Expect(d.OwnerReferences[0].UID).To(Equal(c.UID))
		version := d.ResourceVersion
		statusVersion := c.ResourceVersion
		reconcileWeb(c)
		Expect(webDeployment(c).ResourceVersion).To(Equal(version))
		Expect(c.ResourceVersion).To(Equal(statusVersion))
		Expect(meta.IsStatusConditionFalse(c.Status.Conditions, "Ready")).To(BeTrue())
		Expect(c.Status.Sites[0].Exists).To(BeFalse())
		Expect(k8sClient.Delete(ctx, d)).To(Succeed())
		reconcileWeb(c)
		Expect(webDeployment(c).UID).NotTo(Equal(d.UID))
		Expect(uploads(c).UID).To(Equal(p.UID))
	})

	It("requires current rollout counters and bound storage and preserves condition transition times", func() {
		c := reconcileWeb(webSample())
		d := webDeployment(c)
		p := uploads(c)
		p.Status.Phase = corev1.ClaimBound
		Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
		d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation - 1, Replicas: 1, ReadyReplicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1}
		Expect(k8sClient.Status().Update(ctx, d)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionFalse(c.Status.Conditions, "WebReady")).To(BeTrue())
		d = webDeployment(c)
		d.Status.ObservedGeneration = d.Generation
		Expect(k8sClient.Status().Update(ctx, d)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady")).To(BeTrue())
		Expect(meta.IsStatusConditionFalse(c.Status.Conditions, "Ready")).To(BeTrue())
		Expect(c.Status.Release.Tag).To(Equal(c.Spec.Release.Image.Tag))
		version := c.ResourceVersion
		time.Sleep(1100 * time.Millisecond)
		reconcileWeb(c)
		Expect(c.ResourceVersion).To(Equal(version))
	})

	It("rolls out on Secret revisions and fails closed on a missing key without leaking credentials", func() {
		c := reconcileWeb(webSample())
		old := webDeployment(c).Spec.Template.Annotations[workload.SecretRevision]
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: c.Spec.Crypto.SecretRef.Name}, secret)).To(Succeed())
		secret.Data[c.Spec.Crypto.SecretRef.Key] = []byte("rotated-do-not-disclose")
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		reconcileWeb(c)
		Expect(webDeployment(c).Spec.Template.Annotations[workload.SecretRevision]).NotTo(Equal(old))
		delete(secret.Data, c.Spec.Crypto.SecretRef.Key)
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("SecretKeyInvalid"))
		Expect(c.Status.Conditions).NotTo(ContainElement(HaveField("Message", ContainSubstring("rotated-do-not-disclose"))))
	})

	It("keeps previous immutable configuration revisions for existing ReplicaSets", func() {
		c := reconcileWeb(webSample())
		old := webDeployment(c).Spec.Template.Annotations[workload.ConfigHash]
		c.Spec.Sites[0].Hosts = []string{"changed.example.com"}
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(webDeployment(c).Spec.Template.Annotations[workload.ConfigHash]).NotTo(Equal(old))
		cm := &corev1.ConfigMap{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "php-"+old[:12])}, cm)).To(Succeed())
		Expect(*cm.Immutable).To(BeTrue())
	})

	It("refuses adoption of a foreign Service and a retained PVC from a previous cluster UID", func() {
		c := webSample()
		foreign := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: workload.Name(c, "web"), Namespace: c.Namespace}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 80}}}}
		Expect(k8sClient.Create(ctx, foreign)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("ResourceCollision"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
		Expect(foreign.OwnerReferences).To(BeEmpty())
		p := uploads(c)
		p.Labels[workload.OwnerUID] = "previous-cluster-uid"
		Expect(k8sClient.Update(ctx, p)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("StorageCollision"))
	})

	It("expands storage only when permitted and rejects shrink or class changes", func() {
		c := reconcileWeb(webSample())
		// API server expansion admission requires the provisioned/bound state in envtest.
		p := uploads(c)
		p.Spec.VolumeName = "test-volume"
		Expect(k8sClient.Update(ctx, p)).To(Succeed())
		p.Status.Phase = corev1.ClaimBound
		Expect(k8sClient.Status().Update(ctx, p)).To(Succeed())
		c.Spec.Storage.Uploads.PersistentVolumeClaim.Size = resource.MustParse("21Gi")
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(uploads(c).Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("21Gi")))
		c.Spec.Storage.Uploads.PersistentVolumeClaim.Size = resource.MustParse("19Gi")
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("StorageShrinkForbidden"))
		c.Spec.Storage.Uploads.PersistentVolumeClaim.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcileWeb(c)
		Expect(meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason).To(Equal("ImmutableStorage"))
	})

	It("reconciles Secret metadata events through a real manager", func() {
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: k8sClient.Scheme(), Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0"})
		Expect(err).NotTo(HaveOccurred())
		r := &BitrixClusterReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}
		Expect(r.SetupWithManager(mgr)).To(Succeed())
		managerCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- mgr.Start(managerCtx) }()
		DeferCleanup(func() { stop(); Eventually(done, 10*time.Second).Should(Receive(BeNil())) })
		c := webSample()
		d := &appsv1.Deployment{}
		key := client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}
		Eventually(func() error { return k8sClient.Get(ctx, key, d) }, 15*time.Second).Should(Succeed())
		old := d.Spec.Template.Annotations[workload.SecretRevision]
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: c.Spec.Crypto.SecretRef.Name}, secret)).To(Succeed())
		secret.Annotations = map[string]string{"rotation": "metadata-event"}
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, d)).To(Succeed())
			g.Expect(d.Spec.Template.Annotations[workload.SecretRevision]).NotTo(Equal(old))
		}, 15*time.Second).Should(Succeed())
	})
})
