package controller

import (
"context"
bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
corev1 "k8s.io/api/core/v1"
"sigs.k8s.io/controller-runtime/pkg/client"
)

type blocked struct{ reason, message string }
func (e *blocked) Error() string { return e.message }
func block(reason, message string) error { return &blocked{reason, message} }

// reader is uncached for ownership, Secret data, guarded mutation and read-after-write.
func (r *BitrixClusterReconciler) reader() client.Reader {
if r.APIReader != nil {return r.APIReader}
return r.Client
}

// Rollout observations use the event-driven informer cache.
func (r *BitrixClusterReconciler) observationReader() client.Reader {return r.Client}

// Compatibility adapters allow component builders to remain unchanged.
func (r *BitrixClusterReconciler) apply(ctx context.Context,c *bx.BitrixCluster,configuration any) error {
return r.resourceManager().Apply(ctx,c,configuration)
}
func (r *BitrixClusterReconciler) storage(ctx context.Context,c *bx.BitrixCluster)(*corev1.PersistentVolumeClaim,error){
return r.resourceManager().Storage(ctx,c)
}
