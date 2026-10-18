package controller

import (
	"context"
	"errors"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/kubeops"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Preserve the controller's typed domain error, while delegating Kubernetes
// read/delete mechanics to a small independently testable package.
func (r *BitrixClusterReconciler) deleteOwnedResource(ctx context.Context, c *bx.BitrixCluster, obj client.Object, foreground bool) (bool, error) {
	found, err := kubeops.DeleteOwned(ctx, r.reader(), r.Client, obj, func(live client.Object) bool {
		return controlledByCluster(c, live)
	}, foreground)
	if errors.Is(err, kubeops.ErrForeignResource) {
		return found, block("ResourceCollision", "An application resource is not controlled by this BitrixCluster")
	}
	return found, err
}
