/*
Copyright 2026 M8 Team.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	bitrixv1alpha1 "github.com/m8-team/bitrix-operator/api/v1alpha1"
)

// BitrixClusterReconciler observes BitrixCluster resources. Workload reconciliation
// is intentionally deferred to the runtime iterations documented in the plan.
type BitrixClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=bitrix.m8.team,resources=bitrixclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=bitrix.m8.team,resources=bitrixclusters/status,verbs=get;update;patch

// Reconcile records an honest, stable foundation status without provisioning resources.
func (r *BitrixClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	cluster := &bitrixv1alpha1.BitrixCluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !cluster.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	before := cluster.DeepCopy()
	cluster.Status.ObservedGeneration = cluster.Generation
	cluster.Status.Phase = bitrixv1alpha1.BitrixClusterPhasePending
	meta.SetStatusCondition(&cluster.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: cluster.Generation,
		Reason:             "RuntimeNotImplemented",
		Message:            "API foundation is available; application workloads are not implemented yet.",
	})
	if reflect.DeepEqual(before.Status, cluster.Status) {
		return ctrl.Result{}, nil
	}
	// Optimistic locking prevents an old observation from overwriting a newer status.
	err := r.Status().Patch(ctx, cluster, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	return ctrl.Result{}, err
}

// SetupWithManager registers the controller without optional external CRD dependencies.
func (r *BitrixClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&bitrixv1alpha1.BitrixCluster{}).
		Named("bitrixcluster").
		Complete(r)
}
