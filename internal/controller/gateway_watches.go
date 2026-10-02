package controller

import (
	"context"
	"slices"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gateway "sigs.k8s.io/gateway-api/apis/v1"
)

func gatewayAPIsAvailable(mapper meta.RESTMapper) (bool, error) {
	for _, kind := range []string{gatewayKind, "GatewayClass", "HTTPRoute", "ReferenceGrant"} {
		if _, err := mapper.RESTMapping(schema.GroupKind{Group: gateway.GroupName, Kind: kind}, "v1"); err != nil {
			if meta.IsNoMatchError(err) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}
func watchedSecrets(obj client.Object) []string {
	names := secretNames(obj)
	c := obj.(*bx.BitrixCluster)
	if gatewayEnabled(c) {
		for _, site := range c.Spec.Sites {
			if site.TLS != nil {
				names = append(names, site.TLS.SecretName)
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// Gateway objects and external HTTPRoutes can change acceptance or host reservations.
// Events only enqueue; reconciliation rechecks ownership and current dependencies.
func (r *BitrixClusterReconciler) gatewayRequests(ctx context.Context, obj client.Object) []ctrl.Request {
	clusters := &bx.BitrixClusterList{}
	if err := r.List(ctx, clusters); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Could not list Gateway consumers")
		return nil
	}
	requests := []ctrl.Request{}
	for _, c := range clusters.Items {
		if !gatewayEnabled(&c) || c.Spec.Networking.Gateway.ParentRef == nil {
			continue
		}
		p := c.Spec.Networking.Gateway.ParentRef
		matches := true
		switch source := obj.(type) {
		case *gateway.Gateway:
			matches = source.Namespace == p.Namespace && source.Name == p.Name
		case *gateway.ReferenceGrant:
			matches = source.Namespace == c.Namespace
		case *bx.BitrixCluster:
			matches = gatewayEnabled(source) && source.Spec.Networking.Gateway.ParentRef != nil && *source.Spec.Networking.Gateway.ParentRef == *p
		}
		if matches {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&c)})
		}
	}
	return requests
}
