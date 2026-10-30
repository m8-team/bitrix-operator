package controller

import (
	"context"
	"reflect"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// StatusManager is scoped to one reconciliation. Stages submit observations,
// but only Commit writes the CR status subresource and emits transition events.
type StatusManager struct {
	controller *BitrixClusterReconciler
	cluster    *bx.BitrixCluster
	before     *bx.BitrixCluster
}

func NewStatusManager(controller *BitrixClusterReconciler, cluster *bx.BitrixCluster) *StatusManager {
	return &StatusManager{controller: controller, cluster: cluster, before: cluster.DeepCopy()}
}

func (m *StatusManager) Begin() {
	initializeReconcileStatus(m.cluster)
}

func (m *StatusManager) SetCondition(kind string, status metav1.ConditionStatus, reason, message string) {
	condition(m.cluster, kind, status, reason, message)
}

func (m *StatusManager) Apply(result ComponentResult) {
	applyComponentResult(m.cluster, result)
}

func (m *StatusManager) Resolve(resourceErr, publicationErr error) (ctrl.Result, error) {
	return resolveReconcileResult(m.cluster, resourceErr, publicationErr)
}

// Commit skips unchanged status and protects against concurrent writes through
// optimistic locking. Kubernetes Events follow a successful status patch.
func (m *StatusManager) Commit(ctx context.Context) error {
	preserveConditionTransitions(m.before, m.cluster)
	if reflect.DeepEqual(m.before.Status, m.cluster.Status) {
		return nil
	}
	if err := m.controller.Status().Patch(ctx, m.cluster, client.MergeFromWithOptions(m.before, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}
	m.controller.recordTransition(m.before, m.cluster)
	return nil
}
