package controller

import (
	"errors"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Owned resource deletion", func() {
	It("ignores absent objects and leaves unowned objects untouched", func() {
		c := reconcileWeb(webSample())
		r := &BitrixClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		missing := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "does-not-exist"}}
		found, err := r.resourceManager().DeleteOwned(ctx, c, missing, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeFalse())

		foreign := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "foreign-service"},
			Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}}}
		Expect(k8sClient.Create(ctx, foreign)).To(Succeed())
		found, err = r.resourceManager().DeleteOwned(ctx, c, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: foreign.Name}}, false)
		Expect(found).To(BeTrue())
		var blockedErr *blocked
		Expect(errors.As(err, &blockedErr)).To(BeTrue())
		Expect(blockedErr.reason).To(Equal("ResourceCollision"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
	})

	It("guards a named owned resource and waits for actual termination", func() {
		c := reconcileWeb(webSample())
		r := &BitrixClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		key := client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}
		service := &corev1.Service{}
		Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
		Expect(metav1.IsControlledBy(service, c)).To(BeTrue())
		service.Finalizers = []string{"example.com/hold"}
		Expect(k8sClient.Update(ctx, service)).To(Succeed())

		found, err := r.resourceManager().DeleteOwned(ctx, c, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: service.Name}}, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		current := &corev1.Service{}
		Expect(k8sClient.Get(ctx, key, current)).To(Succeed())
		Expect(current.DeletionTimestamp).NotTo(BeNil())
		found, err = r.resourceManager().DeleteOwned(ctx, c, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: service.Name}}, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		current.Finalizers = nil
		Expect(k8sClient.Update(ctx, current)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(k8sClient.Get(ctx, key, &corev1.Service{}))
		}).Should(BeTrue())
	})

	It("rejects resources that have another cluster UID even with matching names", func() {
		c := reconcileWeb(webSample())
		r := &BitrixClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		wrong := &bx.BitrixCluster{}
		wrong.Name = c.Name
		wrong.Namespace = c.Namespace
		wrong.UID = "a-different-cluster"
		service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: workload.Name(c, "web"), Namespace: c.Namespace}}
		found, err := r.resourceManager().DeleteOwned(ctx, wrong, service, false)
		Expect(found).To(BeTrue())
		var blockedErr *blocked
		Expect(errors.As(err, &blockedErr)).To(BeTrue())
		Expect(blockedErr.reason).To(Equal("ResourceCollision"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(service), &corev1.Service{})).To(Succeed())
	})
})
