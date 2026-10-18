package controller

import (
	"slices"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func validateWorkload(c *bx.BitrixCluster) error {
	s := c.Spec
	if err := validateAutoscaling(c); err != nil {
		return err
	}

	modes := s.Storage.Uploads.PersistentVolumeClaim.AccessModes
	multiPod := s.Runtime.Web.Replicas > 1 || s.Runtime.Cron.Enabled || (workload.AutoscalingEnabled(c) && s.Runtime.Web.Autoscaling.MaxReplicas > 1)
	if slices.Contains(modes, corev1.ReadOnlyMany) {
		return block("InvalidStorageMode", "Uploads storage must be writable")
	}
	if slices.Contains(modes, corev1.ReadWriteOncePod) && (len(modes) != 1 || multiPod) {
		return block("InvalidStorageMode", "ReadWriteOncePod must be the only uploads access mode and can be used only by a single web Pod without cron")
	}
	if multiPod && !slices.Contains(modes, corev1.ReadWriteMany) {
		return block("InvalidStorageMode", "Uploads require ReadWriteMany for multiple web replicas or a separate cron workload")
	}
	if s.Storage.Uploads.PersistentVolumeClaim.Size.Sign() <= 0 || s.Storage.Temporary.SizeLimit.Sign() <= 0 {
		return block("InvalidStorageSize", "Uploads and temporary storage sizes must be positive")
	}
	return nil
}

func deploymentObject() *appsv1.Deployment { return &appsv1.Deployment{} }
