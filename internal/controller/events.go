package controller

import (
	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func eventState(c *bx.BitrixCluster) string {
	ready := meta.FindStatusCondition(c.Status.Conditions, "Ready")
	if ready == nil {
		return ""
	}
	if ready.Status == metav1.ConditionTrue {
		return "ClusterReady"
	}
	if c.Status.Phase == bx.BitrixClusterPhaseDegraded {
		return "ClusterDegraded"
	}
	return "ClusterProgressing"
}

// Emit after a successful status write. Persisted state makes restart/no-op quiet.
func (r *BitrixClusterReconciler) recordTransition(before, current *bx.BitrixCluster) {
	if r.Recorder == nil {
		return
	}
	state := eventState(current)
	if state == "" || state == eventState(before) {
		return
	}
	typeName, note := corev1.EventTypeNormal, "BitrixCluster is progressing; inspect status.conditions for component details"
	switch state {
	case "ClusterReady":
		note = "The current runtime and requested Bitrix site IDs are ready"
	case "ClusterDegraded":
		typeName = corev1.EventTypeWarning
		note = "BitrixCluster is degraded; inspect status.conditions for component details"
	}
	// Fixed vocabulary: no Secret values, condition messages or dependency errors.
	r.Recorder.Eventf(current, nil, typeName, state, "Reconcile", note)
}
