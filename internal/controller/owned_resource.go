package controller

import (
	"context"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// deleteOwnedResource is the single guarded deletion path for named operator
// resources. It never adopts foreign objects and never races against a
// replacement with a different UID or resourceVersion. A true return value
// means the resource exists (possibly already terminating), so callers that
// need dependent Pods gone must continue to wait.
func (r *BitrixClusterReconciler) deleteOwnedResource(ctx context.Context, c *bx.BitrixCluster, obj client.Object, foreground bool) (bool, error) {
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if !controlledByCluster(c, obj) {
		return true, block("ResourceCollision", "An application resource is not controlled by this BitrixCluster")
	}
	if obj.GetDeletionTimestamp() != nil {
		return true, nil
	}
	uid, rv := obj.GetUID(), obj.GetResourceVersion()
	options := []client.DeleteOption{client.Preconditions{UID: &uid, ResourceVersion: &rv}}
	if foreground {
		options = append(options, client.PropagationPolicy(metav1.DeletePropagationForeground))
	}
	return true, client.IgnoreNotFound(r.Delete(ctx, obj, options...))
}
