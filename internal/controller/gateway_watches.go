package controller

import (
	"context"
	"slices"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
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
func gatewayClassNames(obj client.Object) []string {
	g := obj.(*gateway.Gateway)
	return []string{string(g.Spec.GatewayClassName)}
}

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

// gatewayRequestsForParents gathers only consumers of the changed Gateway
// parents. A cluster may consume several routes or gateways, so requests are
// deduplicated before entering the work queue.
func (r *BitrixClusterReconciler) gatewayRequestsForParents(ctx context.Context, parents []string) []ctrl.Request {
	seen := sets.New[client.ObjectKey]()
	requests := make([]ctrl.Request, 0)
	for _, key := range parents {
		clusters := &bx.BitrixClusterList{}
		if err := r.List(ctx, clusters, client.MatchingFields{gatewayParentIndex: key}); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "Could not list Gateway consumers by indexed parent")
			return nil
		}
		for i := range clusters.Items {
			id := client.ObjectKeyFromObject(&clusters.Items[i])
			if seen.Has(id) {
				continue
			}
			seen.Insert(id)
			requests = append(requests, ctrl.Request{NamespacedName: id})
		}
	}
	return requests
}

// Gateway objects and external HTTPRoutes can change acceptance or host reservations.
// Namespace changes matter for AllowedRoutes namespace selectors. These watches
// must never enqueue unrelated BitrixClusters when an index is available.
func (r *BitrixClusterReconciler) gatewayRequests(ctx context.Context, obj client.Object) []ctrl.Request {
	options := []client.ListOption{}
	if r.GatewayParentIndexed {
		switch source := obj.(type) {
		case *gateway.Gateway:
			options = append(options, client.MatchingFields{gatewayParentIndex: gatewayParentKey(source.Namespace, source.Name)})
		case *gateway.ReferenceGrant:
			options = append(options, client.InNamespace(source.Namespace))
		case *gateway.HTTPRoute:
			return r.gatewayRequestsForParents(ctx, routeGatewayParents(source))
		case *gateway.GatewayClass:
			gateways := &gateway.GatewayList{}
			if err := r.List(ctx, gateways, client.MatchingFields{gatewayClassIndex: source.Name}); err != nil {
				ctrl.LoggerFrom(ctx).Error(err, "Could not list Gateways by indexed GatewayClass")
				return nil
			}
			keys := sets.New[string]()
			for i := range gateways.Items {
				gw := &gateways.Items[i]
				keys.Insert(gatewayParentKey(gw.Namespace, gw.Name))
			}
			return r.gatewayRequestsForParents(ctx, sets.List(keys))
		case *bx.BitrixCluster:
			if source.Spec.Networking == nil || source.Spec.Networking.Gateway == nil || source.Spec.Networking.Gateway.ParentRef == nil {
				return nil
			}
			p := source.Spec.Networking.Gateway.ParentRef
			options = append(options, client.MatchingFields{gatewayParentIndex: gatewayParentKey(p.Namespace, p.Name)})
		}
	}
	// WatchesMetadata(Namespaces) provides PartialObjectMetadata. Both that
	// type and the normal typed Namespace object should scope work to tenants
	// actually affected by namespace label or lifecycle changes.
	switch obj.(type) {
	case *corev1.Namespace, *metav1.PartialObjectMetadata:
		options = append(options, client.InNamespace(obj.GetName()))
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
