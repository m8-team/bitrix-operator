package controller

import (
	"context"
	"slices"

	"k8s.io/apimachinery/pkg/util/sets"

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
// Keyed dependency indexes keep Gateway events local to their consumers.
func gatewayParentKey(namespace, name string) string {
	return namespace + "/" + name
}

func clusterGatewayParents(obj client.Object) []string {
	c := obj.(*bx.BitrixCluster)
	if !gatewayEnabled(c) || c.Spec.Networking.Gateway.ParentRef == nil {
		return nil
	}
	p := c.Spec.Networking.Gateway.ParentRef
	return []string{gatewayParentKey(p.Namespace, p.Name)}
}

func routeGatewayParents(obj client.Object) []string {
	route := obj.(*gateway.HTTPRoute)
	keys := sets.New[string]()
	for _, ref := range route.Spec.ParentRefs {
		if ref.Group != nil && *ref.Group != gateway.Group(gateway.GroupName) {
			continue
		}
		if ref.Kind != nil && *ref.Kind != gateway.Kind("Gateway") {
			continue
		}
		namespace := route.Namespace
		if ref.Namespace != nil {
			namespace = string(*ref.Namespace)
		}
		keys.Insert(gatewayParentKey(namespace, string(ref.Name)))
	}
	return sets.List(keys)
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
	options := []client.ListOption{}
	if r.GatewayParentIndexed {
		switch source := obj.(type) {
		case *gateway.Gateway:
			options = append(options, client.MatchingFields{gatewayParentIndex: gatewayParentKey(source.Namespace, source.Name)})
		case *gateway.ReferenceGrant:
			options = append(options, client.InNamespace(source.Namespace))
		case *gateway.HTTPRoute:
			keys := routeGatewayParents(source)
			requests := []ctrl.Request{}
			seen := sets.New[client.ObjectKey]()
			for _, key := range keys {
				clusters := &bx.BitrixClusterList{}
				if err := r.List(ctx, clusters, client.MatchingFields{gatewayParentIndex: key}); err != nil {
					ctrl.LoggerFrom(ctx).Error(err, "Could not list HTTPRoute consumers")
					return nil
				}
				for i := range clusters.Items {
					id := client.ObjectKeyFromObject(&clusters.Items[i])
					if !seen.Has(id) {
						seen.Insert(id)
						requests = append(requests, ctrl.Request{NamespacedName: id})
					}
				}
			}
			return requests
		case *bx.BitrixCluster:
			if source.Spec.Networking == nil || source.Spec.Networking.Gateway == nil || source.Spec.Networking.Gateway.ParentRef == nil {
				return nil
			}
			p := source.Spec.Networking.Gateway.ParentRef
			options = append(options, client.MatchingFields{gatewayParentIndex: gatewayParentKey(p.Namespace, p.Name)})
		}
	}
	clusters := &bx.BitrixClusterList{}
	if err := r.List(ctx, clusters, options...); err != nil {
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
			if source.Spec.Networking == nil || source.Spec.Networking.Gateway == nil || source.Spec.Networking.Gateway.ParentRef == nil {
				matches = false
			} else {
				matches = *source.Spec.Networking.Gateway.ParentRef == *p
			}
		}
		if matches {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&c)})
		}
	}
	return requests
}
