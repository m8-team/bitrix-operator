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
const httpRouteKind gateway.Kind = "HTTPRoute"

// Gateway failures affect publication only; existing internal workloads still reconcile.
func (r *BitrixClusterReconciler) reconcileGateway(ctx context.Context, c *bx.BitrixCluster, runtimeValid bool) (ComponentResult, error) {
	result := ComponentResult{}
	if !gatewayEnabled(c) {
		if err := r.pruneRoutes(ctx, c, nil); err != nil {
			return result, err
		}
		clearPublishedSiteURLs(c, &result)
		return result, nil
	}
	if !runtimeValid {
		// Preserve last-known-good routes during reconciliation failures.
		// An API outage, SSA conflict or temporarily missing Secret must not
		// remove ingress for otherwise healthy existing web workloads.
		result.SetCondition(c.Generation, "GatewayReady", metav1.ConditionFalse, "RuntimeInvalid", "Publication waits for a valid runtime configuration")
		return result, nil
	}
	p, err := r.publication(ctx, c)
	if err == nil {
		err = r.hostConflicts(ctx, c, p)
	}
	if err != nil {
		result.SetCondition(c.Generation, "GatewayReady", metav1.ConditionFalse, "PublicationFailed", "Could not validate Gateway publication")
		if issue, ok := errors.AsType[*blocked](err); ok {
			// A known invalid publication is explicitly withdrawn.
			result.SetCondition(c.Generation, "GatewayReady", metav1.ConditionFalse, issue.reason, issue.message)
			if cleanupErr := r.pruneRoutes(ctx, c, nil); cleanupErr != nil {
				return result, &gatewayCleanupError{reason: issue.reason, message: issue.message, cause: cleanupErr}
			}
			clearPublishedSiteURLs(c, &result)
			return result, err
		}
		// A transient API/transport error must not delete last-known-good routes.
		return result, err
	}
	desired := desiredRoutes(c)
	names := make([]string, 0, len(desired))
	for _, route := range desired {
		names = append(names, route.Name)
	}
	// Do not remove last-known-good routes until every desired route has
	// been applied and read back successfully. A create/SSA/read failure
	// must leave the old publication intact for the next retry.
	ready := gatewayProgrammed(p)
	for _, route := range desired {
		if err = r.resourceManager().Apply(ctx, c, route); err != nil {
			return result, err
		}
		live := &gateway.HTTPRoute{}
		if err = r.reader().Get(ctx, client.ObjectKeyFromObject(route), live); err != nil {
			return result, err
		}
		if !routeAccepted(live, p, c.Spec.Networking.Gateway.ParentRef) {
			ready = false
		}
	}
	// Keep superseded routes serving the desired hostnames until the
	// replacement HTTPRoutes and Gateway/listener report current readiness.
	// Routes serving only removed hostnames can be deleted immediately.
	var pendingHosts []gateway.Hostname
	if !ready {
		for _, route := range desired {
			pendingHosts = append(pendingHosts, route.Spec.Hostnames...)
		}
	}
	if err = r.pruneRoutes(ctx, c, names, pendingHosts...); err != nil {
		return result, err
	}
	if !ready {
		result.SetCondition(c.Generation, "GatewayReady", metav1.ConditionFalse, "RoutePending", "Waiting for current Gateway/listener and HTTPRoute Accepted/ResolvedRefs conditions")
		return result, nil
	}
	result.SetCondition(c.Generation, "GatewayReady", metav1.ConditionTrue, "RoutesReady", "The Gateway has accepted all current HTTPRoutes and resolved their references")
	if meta.IsStatusConditionTrue(c.Status.Conditions, "SitesValidated") && meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady") {
		sites := result.SitesFor(c.Status.Sites)
		for i, site := range c.Spec.Sites {
			// A same-generation retry preserves site status, so never append
			// the same URL a second time to the schema's set-valued field.
			sites[i].URLs = nil
			scheme, standard := "http", gateway.PortNumber(80)
			if p.listener.Protocol == gateway.HTTPSProtocolType {
				scheme, standard = "https", 443
			}
			for _, host := range site.Hosts {
				authority := host
				if p.listener.Port != standard {
					authority = host + ":" + strconv.Itoa(int(p.listener.Port))
				}
				sites[i].URLs = append(sites[i].URLs, scheme+"://"+authority)
			}
			slices.Sort(sites[i].URLs)
		}
	}
	return result, nil
}

// clearPublishedSiteURLs removes stale public URL evidence only after the
// corresponding routes were successfully withdrawn. Transient Gateway
// failures retain last-known-good routes and URLs.
func clearPublishedSiteURLs(c *bx.BitrixCluster, result *ComponentResult) {
	for i := range result.SitesFor(c.Status.Sites) {
		result.Sites[i].URLs = nil
	}
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

// pruneRoutes deletes obsolete owned routes with UID/RV preconditions. When a
// replacement is still pending, old routes overlapping pendingHosts are kept
// as last-known-good publication; explicit deletion and invalid publication
// pass no pendingHosts so they always withdraw the old routes.
func (r *BitrixClusterReconciler) pruneRoutes(ctx context.Context, c *bx.BitrixCluster, keep []string, pendingHosts ...gateway.Hostname) error {
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
		if routeOverlapsHostnames(route, pendingHosts) {
			continue
		}
		uid, rv := route.UID, route.ResourceVersion
		if err := client.IgnoreNotFound(r.Delete(ctx, route, client.Preconditions{UID: &uid, ResourceVersion: &rv})); err != nil {
			return err
		}
	}
	return nil
}

// routeOverlapsHostnames conservatively keeps a superseded route if it
// could still serve a hostname that a pending replacement is meant to serve.
// An empty hostname list on the old route means it could match any hostname.
func routeOverlapsHostnames(old *gateway.HTTPRoute, pendingHosts []gateway.Hostname) bool {
	if len(pendingHosts) == 0 {
		return false
	}
	if len(old.Spec.Hostnames) == 0 {
		return true
	}
	for _, previous := range old.Spec.Hostnames {
		for _, next := range pendingHosts {
			if listenerHostnameMatches(string(previous), string(next)) ||
				listenerHostnameMatches(string(next), string(previous)) {
				return true
			}
		}
	}
	return false
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
func (r *BitrixClusterReconciler) publicationPort(ctx context.Context, c *bx.BitrixCluster) (int32, error) {
	if !gatewayEnabled(c) || !r.GatewayAPIAvailable || c.Spec.Networking.Gateway.ParentRef == nil {
		return 0, nil
	}
	p := c.Spec.Networking.Gateway.ParentRef
	g := &gateway.Gateway{}
	if err := r.observationReader().Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: p.Name}, g); err != nil {
		// Never render a different nginx config from an unobserved listener.
		// In particular, a cache/API outage must not start an unrelated rollout.
		return 0, fmt.Errorf("observe Gateway %s/%s listener %s: %w", p.Namespace, p.Name, p.SectionName, err)
	}
	for _, l := range g.Spec.Listeners {
		if string(l.Name) == p.SectionName {
			return l.Port, nil
		}
	}
	// A missing listener is invalid publication, not a transport error;
	// reconcileGateway reports the specific reason and withdraws routes.
	return 0, nil
}
