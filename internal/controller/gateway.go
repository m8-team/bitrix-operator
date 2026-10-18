package controller

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gateway "sigs.k8s.io/gateway-api/apis/v1"
)

const gatewayKind = "Gateway"

// Gateway failures affect publication only; existing internal workloads still reconcile.
func (r *BitrixClusterReconciler) reconcileGateway(ctx context.Context, c *bx.BitrixCluster, runtimeValid bool) error {
	if !gatewayEnabled(c) {
		return r.pruneRoutes(ctx, c, nil)
	}
	if !runtimeValid {
		// Preserve last-known-good routes during reconciliation failures.
		// An API outage, SSA conflict or temporarily missing Secret must not
		// remove ingress for otherwise healthy existing web workloads.
		condition(c, "GatewayReady", metav1.ConditionFalse, "RuntimeInvalid", "Publication waits for a valid runtime configuration")
		return nil
	}
	p, err := r.publication(ctx, c)
	if err == nil {
		err = r.hostConflicts(ctx, c, p)
	}
	if err != nil {
		condition(c, "GatewayReady", metav1.ConditionFalse, "PublicationFailed", "Could not validate Gateway publication")
		if issue, ok := errors.AsType[*blocked](err); ok {
			condition(c, "GatewayReady", metav1.ConditionFalse, issue.reason, issue.message)
			err = nil
		}
		return errors.Join(err, r.pruneRoutes(ctx, c, nil))
	}
	desired := desiredRoutes(c)
	names := make([]string, 0, len(desired))
	for _, route := range desired {
		names = append(names, route.Name)
	}
	if err = r.pruneRoutes(ctx, c, names); err != nil {
		return err
	}
	ready := gatewayProgrammed(p)
	for _, route := range desired {
		if err = r.apply(ctx, c, route); err != nil {
			return err
		}
		live := &gateway.HTTPRoute{}
		if err = r.reader().Get(ctx, client.ObjectKeyFromObject(route), live); err != nil {
			return err
		}
		if !routeAccepted(live, p, c.Spec.Networking.Gateway.ParentRef) {
			ready = false
		}
	}
	if !ready {
		condition(c, "GatewayReady", metav1.ConditionFalse, "RoutePending", "Waiting for current Gateway/listener and HTTPRoute Accepted/ResolvedRefs conditions")
		return nil
	}
	condition(c, "GatewayReady", metav1.ConditionTrue, "RoutesReady", "The Gateway has accepted all current HTTPRoutes and resolved their references")
	if meta.IsStatusConditionTrue(c.Status.Conditions, "SitesValidated") && meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady") {
		for i, site := range c.Spec.Sites {
			scheme, standard := "http", gateway.PortNumber(80)
			if p.listener.Protocol == gateway.HTTPSProtocolType {
				scheme, standard = "https", 443
			}
			for _, host := range site.Hosts {
				authority := host
				if p.listener.Port != standard {
					authority = host + ":" + strconv.Itoa(int(p.listener.Port))
				}
				c.Status.Sites[i].URLs = append(c.Status.Sites[i].URLs, scheme+"://"+authority)
			}
			slices.Sort(c.Status.Sites[i].URLs)
		}
	}
	return nil
}

func desiredRoutes(c *bx.BitrixCluster) []*gateway.HTTPRoute {
	routes := []*gateway.HTTPRoute{}
	for _, site := range c.Spec.Sites {
		hosts := slices.Clone(site.Hosts)
		slices.Sort(hosts)
		for offset := 0; offset < len(hosts); offset += 16 {
			chunk := site
			chunk.Hosts = hosts[offset:min(offset+16, len(hosts))]
			route := workload.HTTPRoute(c, chunk)
			if offset > 0 {
				route.Name = workload.RouteName(c, fmt.Sprintf("%s#%d", site.ID, offset/16))
			}
			routes = append(routes, route)
		}
	}
	return routes
}

func (r *BitrixClusterReconciler) pruneRoutes(ctx context.Context, c *bx.BitrixCluster, keep []string) error {
	if !r.GatewayAPIAvailable {
		return nil
	}
	routes := &gateway.HTTPRouteList{}
	if err := r.reader().List(ctx, routes, client.InNamespace(c.Namespace)); err != nil {
		return err
	}
	for i := range routes.Items {
		route := &routes.Items[i]
		if !controlledByCluster(c, route) || slices.Contains(keep, route.Name) || !route.DeletionTimestamp.IsZero() {
			continue
		}
		uid, rv := route.UID, route.ResourceVersion
		if err := client.IgnoreNotFound(r.Delete(ctx, route, client.Preconditions{UID: &uid, ResourceVersion: &rv})); err != nil {
			return err
		}
	}
	return nil
}

func currentConditions(conditions []metav1.Condition, generation int64, kinds ...string) bool {
	for _, kind := range kinds {
		c := meta.FindStatusCondition(conditions, kind)
		if c == nil || c.Status != metav1.ConditionTrue || c.ObservedGeneration != generation {
			return false
		}
	}
	return true
}
func gatewayProgrammed(p *publication) bool {
	g := p.gateway
	if !currentConditions(g.Status.Conditions, g.Generation, "Accepted", "Programmed") {
		return false
	}
	for _, l := range g.Status.Listeners {
		if l.Name == p.listener.Name {
			return currentConditions(l.Conditions, g.Generation, "Accepted", "Programmed", "ResolvedRefs")
		}
	}
	return false
}
func targetsGateway(ref gateway.ParentReference, routeNamespace string, p *bx.GatewayParentReference, port gateway.PortNumber) bool {
	namespace := routeNamespace
	if ref.Namespace != nil {
		namespace = string(*ref.Namespace)
	}
	return (ref.Group == nil || *ref.Group == gateway.Group(gateway.GroupName)) && (ref.Kind == nil || *ref.Kind == gatewayKind) && string(ref.Name) == p.Name && namespace == p.Namespace &&
		(ref.SectionName == nil || string(*ref.SectionName) == p.SectionName) && (ref.Port == nil || *ref.Port == port)
}
func routeAccepted(route *gateway.HTTPRoute, p *publication, parent *bx.GatewayParentReference) bool {
	for _, status := range route.Status.Parents {
		if status.ControllerName == p.controller && status.ParentRef.SectionName != nil && targetsGateway(status.ParentRef, route.Namespace, parent, p.listener.Port) &&
			currentConditions(status.Conditions, route.Generation, "Accepted", "ResolvedRefs") {
			return true
		}
	}
	return false
}

func (r *BitrixClusterReconciler) hostConflicts(ctx context.Context, c *bx.BitrixCluster, p *publication) error {
	parent := c.Spec.Networking.Gateway.ParentRef
	clusters := &bx.BitrixClusterList{}
	if r.GatewayParentIndexed {
		if err := r.List(ctx, clusters, client.MatchingFields{gatewayParentIndex: gatewayParentKey(parent.Namespace, parent.Name)}); err != nil {
			return err
		}
	} else if err := r.reader().List(ctx, clusters); err != nil {
		return err
	}
	for _, other := range clusters.Items {
		if other.UID == c.UID || !gatewayEnabled(&other) || !other.DeletionTimestamp.IsZero() {
			continue
		}
		ref := other.Spec.Networking.Gateway.ParentRef
		if ref == nil || *ref != *parent {
			continue
		}
		for _, site := range other.Spec.Sites {
			for _, host := range site.Hosts {
				if clusterHasHost(c, host) {
					return block("HostnameConflict", "Another BitrixCluster requests a hostname on the same Gateway listener")
				}
			}
		}
	}
	routes := &gateway.HTTPRouteList{}
	if r.GatewayParentIndexed {
		if err := r.List(ctx, routes, client.MatchingFields{routeParentIndex: gatewayParentKey(parent.Namespace, parent.Name)}); err != nil {
			return err
		}
	} else if err := r.reader().List(ctx, routes); err != nil {
		return err
	}
	for _, route := range routes.Items {
		if controlledByCluster(c, &route) || !route.DeletionTimestamp.IsZero() {
			continue
		}
		attached := false
		for _, ref := range route.Spec.ParentRefs {
			if targetsGateway(ref, route.Namespace, parent, p.listener.Port) {
				attached = true
			}
		}
		if !attached {
			continue
		}
		if len(route.Spec.Hostnames) == 0 {
			return block("HostnameConflict", "An existing HTTPRoute matches all hostnames on the selected listener")
		}
		for _, host := range route.Spec.Hostnames {
			if clusterHasHost(c, string(host)) {
				return block("HostnameConflict", "An existing HTTPRoute overlaps a hostname on the selected listener")
			}
		}
	}
	return nil
}
func clusterHasHost(c *bx.BitrixCluster, pattern string) bool {
	for _, site := range c.Spec.Sites {
		for _, host := range site.Hosts {
			if listenerHostnameMatches(pattern, host) {
				return true
			}
		}
	}
	return false
}

// The listener port enters the native nginx config, so port changes trigger rollout.
// Detailed validation/errors are handled by reconcileGateway, independently of web health.
func (r *BitrixClusterReconciler) publicationPort(ctx context.Context, c *bx.BitrixCluster) int32 {
	if !gatewayEnabled(c) || !r.GatewayAPIAvailable || c.Spec.Networking.Gateway.ParentRef == nil {
		return 0
	}
	p := c.Spec.Networking.Gateway.ParentRef
	g := &gateway.Gateway{}
	if r.reader().Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: p.Name}, g) != nil {
		return 0
	}
	for _, l := range g.Spec.Listeners {
		if string(l.Name) == p.SectionName {
			return l.Port
		}
	}
	return 0
}
