package controller

import (
	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Controller helpers", func() {
	It("observes only the current Deployment generation as ready", func() {
		d := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Generation: 2},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration: 1,
				Replicas:           2,
				ReadyReplicas:      2,
				UpdatedReplicas:    2,
				AvailableReplicas:  2,
			},
		}
		rollout := observeDeployment(d, 2)
		Expect(rollout.status.DesiredReplicas).To(Equal(int32(2)))
		Expect(rollout.ready).To(BeFalse())

		d.Status.ObservedGeneration = d.Generation
		rollout = observeDeployment(d, 2)
		Expect(rollout.ready).To(BeTrue())
	})

	It("reports progress deadline failures only for the observed generation", func() {
		d := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Generation: 2},
			Status: appsv1.DeploymentStatus{
				ObservedGeneration: 1,
				Conditions: []appsv1.DeploymentCondition{{
					Type:   appsv1.DeploymentProgressing,
					Status: corev1.ConditionFalse,
					Reason: progressDeadlineExceeded,
				}},
			},
		}
		Expect(observeDeployment(d, 1).deadlineExceeded).To(BeFalse())
		d.Status.ObservedGeneration = d.Generation
		Expect(observeDeployment(d, 1).deadlineExceeded).To(BeTrue())
	})

	It("accepts only the exact BitrixCluster controller owner", func() {
		c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{
			Name: "portal",
			UID:  types.UID("cluster-uid"),
		}}
		controller := true
		d := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{OwnerReferences: []metav1.OwnerReference{{
			APIVersion: bx.GroupVersion.String(),
			Kind:       bitrixClusterKind,
			Name:       c.Name,
			UID:        c.UID,
			Controller: &controller,
		}}}}
		Expect(controlledByCluster(c, d)).To(BeTrue())
		d.OwnerReferences[0].UID = types.UID("other")
		Expect(controlledByCluster(c, d)).To(BeFalse())
	})
})
