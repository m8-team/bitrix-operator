package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"strings"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gateway "sigs.k8s.io/gateway-api/apis/v1"
)

type publication struct {
	gateway    *gateway.Gateway
	listener   gateway.Listener
	controller gateway.GatewayController
}

func gatewayEnabled(c *bx.BitrixCluster) bool {
	return c.Spec.Networking != nil && c.Spec.Networking.Gateway != nil && c.Spec.Networking.Gateway.Enabled
}

func listenerHostnameMatches(pattern, host string) bool {
	return pattern == "" || pattern == host || (strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:]) && len(host) > len(pattern)-1)
}

func (r *BitrixClusterReconciler) publication(ctx context.Context, c *bx.BitrixCluster) (*publication, error) {
	if !r.GatewayAPIAvailable {
		return nil, block("DependencyMissing", "Gateway API v1 CRDs were not available at manager startup; install them and restart the manager")
	}
	p := c.Spec.Networking.Gateway.ParentRef
	if p == nil {
		return nil, block("InvalidGateway", "Gateway parentRef is required")
	}
	g := &gateway.Gateway{}
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: p.Namespace, Name: p.Name}, g); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, block("GatewayMissing", "The referenced Gateway does not exist")
		}
		return nil, err
	}
	if !g.DeletionTimestamp.IsZero() {
		return nil, block("GatewayDeleting", "The referenced Gateway is being deleted")
	}
	var listener *gateway.Listener
	for _, l := range g.Spec.Listeners {
		if string(l.Name) == p.SectionName {
			listener = &l
			break
		}
	}
	if listener == nil {
		return nil, block("ListenerMissing", "The selected Gateway listener does not exist")
	}
	if listener.Protocol != gateway.HTTPProtocolType && listener.Protocol != gateway.HTTPSProtocolType {
		return nil, block("UnsupportedListener", "Only HTTP and HTTPS termination listeners are supported")
	}
	if err := r.allowedRoutes(ctx, c, g, listener); err != nil {
		return nil, err
	}
	for _, site := range c.Spec.Sites {
		for _, host := range site.Hosts {
			if listener.Hostname != nil && !listenerHostnameMatches(string(*listener.Hostname), host) {
				return nil, block("HostnameMismatch", "A site hostname is outside the selected listener hostname")
			}
		}
		if listener.Protocol == gateway.HTTPProtocolType && site.TLS != nil {
			return nil, block("TLSListenerMismatch", "A site with TLS requires an HTTPS listener")
		}
		if listener.Protocol == gateway.HTTPSProtocolType {
			if err := r.siteTLS(ctx, c, g, listener, site); err != nil {
				return nil, err
			}
		}
	}
	class := &gateway.GatewayClass{}
	if err := r.reader().Get(ctx, client.ObjectKey{Name: string(g.Spec.GatewayClassName)}, class); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, block("GatewayClassMissing", "The GatewayClass does not exist")
		}
		return nil, err
	}
	return &publication{gateway: g, listener: *listener, controller: class.Spec.ControllerName}, nil
}

func (r *BitrixClusterReconciler) allowedRoutes(ctx context.Context, c *bx.BitrixCluster, g *gateway.Gateway, l *gateway.Listener) error {
	from := gateway.NamespacesFromSame
	allowed := l.AllowedRoutes
	if allowed != nil {
		if len(allowed.Kinds) > 0 {
			found := false
			for _, kind := range allowed.Kinds {
				if kind.Kind == "HTTPRoute" && (kind.Group == nil || *kind.Group == gateway.Group(gateway.GroupName)) {
					found = true
				}
			}
			if !found {
				return block("RoutesNotAllowed", "The listener does not allow HTTPRoute")
			}
		}
		if allowed.Namespaces != nil && allowed.Namespaces.From != nil {
			from = *allowed.Namespaces.From
		}
	}
	if from == gateway.NamespacesFromAll || (from == gateway.NamespacesFromSame && c.Namespace == g.Namespace) {
		return nil
	}
	if from == gateway.NamespacesFromSelector && allowed.Namespaces.Selector != nil {
		selector, err := metav1.LabelSelectorAsSelector(allowed.Namespaces.Selector)
		if err != nil {
			return block("RoutesNotAllowed", "The listener namespace selector is invalid")
		}
		ns := &corev1.Namespace{}
		if err = r.reader().Get(ctx, client.ObjectKey{Name: c.Namespace}, ns); err != nil {
			return err
		}
		if selector.Matches(labels.Set(ns.Labels)) {
			return nil
		}
	}
	return block("RoutesNotAllowed", "The listener does not allow routes from the BitrixCluster namespace")
}

func (r *BitrixClusterReconciler) siteTLS(ctx context.Context, c *bx.BitrixCluster, g *gateway.Gateway, l *gateway.Listener, site bx.BitrixSiteSpec) error {
	if site.TLS == nil {
		return block("TLSRequired", "Every site requires a TLS Secret for an HTTPS listener")
	}
	if l.TLS == nil || (l.TLS.Mode != nil && *l.TLS.Mode != gateway.TLSModeTerminate) {
		return block("UnsupportedListener", "HTTPS requires TLS termination on the Gateway")
	}
	matched := false
	for _, ref := range l.TLS.CertificateRefs {
		ns := g.Namespace
		if ref.Namespace != nil {
			ns = string(*ref.Namespace)
		}
		if (ref.Group == nil || *ref.Group == "") && (ref.Kind == nil || *ref.Kind == "Secret") && ns == c.Namespace && string(ref.Name) == site.TLS.SecretName {
			matched = true
		}
	}
	if !matched {
		return block("CertificateRefMismatch", "The listener does not reference the site TLS Secret in the BitrixCluster namespace")
	}
	if c.Namespace != g.Namespace {
		if err := r.certificateGrant(ctx, c, g.Namespace, site.TLS.SecretName); err != nil {
			return err
		}
	}
	secret := &corev1.Secret{}
	if err := r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: site.TLS.SecretName}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return block("TLSSecretMissing", "A site TLS Secret does not exist")
		}
		return err
	}
	return validateCertificate(secret, site.Hosts)
}

func validateCertificate(secret *corev1.Secret, hosts []string) error {
	if secret.Type != corev1.SecretTypeTLS {
		return block("InvalidCertificate", "A site Secret must have type kubernetes.io/tls")
	}
	pair, err := tls.X509KeyPair(secret.Data[corev1.TLSCertKey], secret.Data[corev1.TLSPrivateKeyKey])
	if err != nil || len(pair.Certificate) == 0 {
		return block("InvalidCertificate", "The site certificate or private key is invalid or mismatched")
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return block("InvalidCertificate", "The site certificate cannot be parsed")
	}
	now := time.Now()
	if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
		return block("CertificateExpired", "The site certificate is expired or not yet valid")
	}
	for _, host := range hosts {
		if cert.VerifyHostname(host) != nil {
			return block("CertificateHostnameMismatch", "The site certificate does not cover every site hostname")
		}
	}
	if len(cert.ExtKeyUsage) > 0 {
		server := false
		for _, usage := range cert.ExtKeyUsage {
			if usage == x509.ExtKeyUsageServerAuth || usage == x509.ExtKeyUsageAny {
				server = true
			}
		}
		if !server {
			return block("InvalidCertificate", "The certificate is not valid for server authentication")
		}
	}
	return nil
}

func (r *BitrixClusterReconciler) certificateGrant(ctx context.Context, c *bx.BitrixCluster, fromNS, name string) error {
	grants := &gateway.ReferenceGrantList{}
	if err := r.reader().List(ctx, grants, client.InNamespace(c.Namespace)); err != nil {
		return err
	}
	for _, grant := range grants.Items {
		if !grant.DeletionTimestamp.IsZero() {
			continue
		}
		from := false
		for _, ref := range grant.Spec.From {
			if ref.Group == gateway.Group(gateway.GroupName) && ref.Kind == gatewayKind && string(ref.Namespace) == fromNS {
				from = true
			}
		}
		if !from {
			continue
		}
		for _, to := range grant.Spec.To {
			if to.Group == "" && to.Kind == "Secret" && (to.Name == nil || string(*to.Name) == name) {
				return nil
			}
		}
	}
	return block("ReferenceNotPermitted", "A cross-namespace Gateway certificate reference requires a matching ReferenceGrant")
}
