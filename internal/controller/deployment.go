package controller

import (
	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	bitrixClusterKind        = "BitrixCluster"
	deploymentKind           = "Deployment"
	phpContainer             = "php"
	progressDeadlineExceeded = "ProgressDeadlineExceeded"
)

type deploymentRollout struct {
	status           bx.WorkloadStatus
	ready            bool
	deadlineExceeded bool
}

func observeDeployment(d *appsv1.Deployment, desired int32) deploymentRollout {
	s := d.Status
	result := deploymentRollout{
		status: bx.WorkloadStatus{
			DesiredReplicas:   desired,
			ReadyReplicas:     s.ReadyReplicas,
			UpdatedReplicas:   s.UpdatedReplicas,
			AvailableReplicas: s.AvailableReplicas,
		},
	}
	current := s.ObservedGeneration >= d.Generation
	result.ready = current &&
		s.Replicas == desired &&
		s.ReadyReplicas == desired &&
		s.UpdatedReplicas == desired &&
		s.AvailableReplicas == desired
	if !current {
		return result
	}
	for _, condition := range s.Conditions {
		if condition.Type == appsv1.DeploymentProgressing &&
			condition.Status == corev1.ConditionFalse &&
			condition.Reason == progressDeadlineExceeded {
			result.deadlineExceeded = true
			break
		}
	}
	return result
}

func controlledByCluster(c *bx.BitrixCluster, obj metav1.Object) bool {
	owner := metav1.GetControllerOf(obj)
	return owner != nil &&
		owner.UID == c.UID &&
		owner.Kind == bitrixClusterKind &&
		owner.APIVersion == bx.GroupVersion.String()
}
