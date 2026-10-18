// Package kubeops contains guarded Kubernetes resource operations that do not
// depend on Bitrix reconciler state, rendering, or status Conditions.
package kubeops

import (
	"context"
	"errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var ErrForeignResource = errors.New("resource is not controlled by the expected owner")

// ControlledBy checks UID, kind and API version. The UID check prevents
// accidental adoption when a BitrixCluster name is reused after deletion.
func ControlledBy(obj metav1.Object, uid metav1.Object, apiVersion, kind string) bool {
	owner := metav1.GetControllerOf(obj)
	return owner != nil && owner.UID == uid.GetUID() && owner.Kind == kind && owner.APIVersion == apiVersion
}

// DeleteOwned returns true while an owned object is present, including when
// it is already terminating. A missing object returns (false, nil). Mutations
// are guarded by the observed UID and resourceVersion, preventing deletion of
// a concurrently replaced object. Use an uncached Reader at sensitive call sites.
func DeleteOwned(ctx context.Context, reader client.Reader, writer client.Client, obj client.Object, owns func(client.Object) bool, foreground bool) (bool, error) {
	if err := reader.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return false, client.IgnoreNotFound(err)
	}
	if !owns(obj) {
		return true, ErrForeignResource
	}
	if obj.GetDeletionTimestamp() != nil {
		return true, nil
	}
	uid, rv := obj.GetUID(), obj.GetResourceVersion()
	options := []client.DeleteOption{client.Preconditions{UID: &uid, ResourceVersion: &rv}}
	if foreground {
		options = append(options, client.PropagationPolicy(metav1.DeletePropagationForeground))
	}
	return true, client.IgnoreNotFound(writer.Delete(ctx, obj, options...))
}
