package workload

import (
	"crypto/sha256"
	"fmt"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gateway "sigs.k8s.io/gateway-api/apis/v1"
)

func RouteName(c *bx.BitrixCluster, site string) string {
	hash := sha256.Sum256([]byte(site))
	return Name(c, fmt.Sprintf("route-%x", hash[:8]))
}

func HTTPRoute(c *bx.BitrixCluster, site bx.BitrixSiteSpec) *gateway.HTTPRoute {
	p := c.Spec.Networking.Gateway.ParentRef
	hosts := make([]gateway.Hostname, 0, len(site.Hosts))
	for _, h := range site.Hosts {
		hosts = append(hosts, gateway.Hostname(h))
	}
	return &gateway.HTTPRoute{TypeMeta: metav1.TypeMeta{APIVersion: gateway.GroupVersion.String(), Kind: "HTTPRoute"}, ObjectMeta: metav1.ObjectMeta{
		Name: RouteName(c, site.ID), Namespace: c.Namespace, Labels: labels(c), OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(c, bx.GroupVersion.WithKind("BitrixCluster"))}},
		Spec: gateway.HTTPRouteSpec{CommonRouteSpec: gateway.CommonRouteSpec{ParentRefs: []gateway.ParentReference{{Group: ptr.To(gateway.Group(gateway.GroupName)), Kind: ptr.To(gateway.Kind("Gateway")), Name: gateway.ObjectName(p.Name), Namespace: new(gateway.Namespace(p.Namespace)), SectionName: new(gateway.SectionName(p.SectionName))}}}, Hostnames: hosts,
			Rules: []gateway.HTTPRouteRule{{Matches: []gateway.HTTPRouteMatch{{Path: &gateway.HTTPPathMatch{Type: ptr.To(gateway.PathMatchPathPrefix), Value: new("/")}}}, BackendRefs: []gateway.HTTPBackendRef{{BackendRef: gateway.BackendRef{BackendObjectReference: gateway.BackendObjectReference{Group: ptr.To(gateway.Group("")), Kind: ptr.To(gateway.Kind("Service")), Name: gateway.ObjectName(Name(c, "web")), Port: ptr.To(gateway.PortNumber(80))}, Weight: new(int32(1))}}}}}}}
}
