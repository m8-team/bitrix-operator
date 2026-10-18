package controller

import (
	"github.com/m8-team/bitrix-operator/internal/workload"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Versioned runtime ConfigMap garbage collection", func() {
	It("deletes only unreferenced owned revisions and preserves rollback references", func() {
		c := reconcileWeb(webSample())
		current := webDeployment(c).Spec.Template.Spec.Volumes
		var currentConfig string
		for _, v := range current {
			if v.ConfigMap != nil {
				currentConfig = v.ConfigMap.Name
				break
			}
		}
		Expect(currentConfig).NotTo(BeEmpty())
		legacy := workload.Name(c, "nginx-abcdef012345")
		retired := workload.Name(c, "php-abcdef012345")
		unrelated := workload.Name(c, "other-abcdef012345")
		for _, name := range []string{legacy, retired, unrelated} {
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: c.Namespace,
				Labels: map[string]string{workload.OwnerUID: string(c.UID)},
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(c, c.GroupVersionKind())}}}
			Expect(k8sClient.Create(ctx, cm)).To(Succeed())
		}
		// A retained ReplicaSet still needs this revision for rollback.
		rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{GenerateName: "previous-", Namespace: c.Namespace,
			Labels: map[string]string{workload.OwnerUID: string(c.UID)}},
			Spec: appsv1.ReplicaSetSpec{Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"test": "previous"}},
				Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"test": "previous"}},
					Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "busybox"}},
						Volumes: []corev1.Volume{{Name: "settings", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: legacy}}}}}}}}}
		Expect(k8sClient.Create(ctx, rs)).To(Succeed())
		b, err := render.Render(c.Spec)
		Expect(err).NotTo(HaveOccurred())
		r := &BitrixClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		Expect(r.pruneConfigMaps(ctx, c, b)).To(Succeed())
		for _, name := range []string{currentConfig, legacy, unrelated} {
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: name}, cm)).To(Succeed())
		}
		gone := &corev1.ConfigMap{}
		Expect(client.IgnoreNotFound(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: retired}, gone))).To(Succeed())
		Expect(gone.UID).To(BeEmpty())
	})
})
