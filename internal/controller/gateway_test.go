package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gateway "sigs.k8s.io/gateway-api/apis/v1"
)

func gatewaySample(c *bx.BitrixCluster) *gateway.Gateway {
	class := &gateway.GatewayClass{ObjectMeta: metav1.ObjectMeta{GenerateName: "fixture-"}, Spec: gateway.GatewayClassSpec{ControllerName: "example.com/test-controller"}}
	Expect(k8sClient.Create(ctx, class)).To(Succeed())
	DeferCleanup(func() { Expect(k8sClient.Delete(ctx, class)).To(Succeed()) })
	g := &gateway.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: c.Namespace}, Spec: gateway.GatewaySpec{GatewayClassName: gateway.ObjectName(class.Name), Listeners: []gateway.Listener{{Name: "web", Protocol: gateway.HTTPProtocolType, Port: 80}}}}
	Expect(k8sClient.Create(ctx, g)).To(Succeed())
	c.Spec.Networking = &bx.NetworkingSpec{Gateway: &bx.GatewaySpec{Enabled: true, ParentRef: &bx.GatewayParentReference{Name: g.Name, Namespace: g.Namespace, SectionName: "web"}}}
	Expect(k8sClient.Update(ctx, c)).To(Succeed())
	return g
}
func reconcilePublished(c *bx.BitrixCluster) {
	r := &BitrixClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), GatewayAPIAvailable: true}
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(c), c)).To(Succeed())
}
func publicationReason(c *bx.BitrixCluster) string {
	return meta.FindStatusCondition(c.Status.Conditions, "GatewayReady").Reason
}
func readyConditions(generation int64, kinds ...string) []metav1.Condition {
	result := make([]metav1.Condition, 0, len(kinds))
	for _, kind := range kinds {
		result = append(result, metav1.Condition{Type: kind, Status: metav1.ConditionTrue, Reason: "Accepted", Message: "fixture", ObservedGeneration: generation, LastTransitionTime: metav1.Now()})
	}
	return result
}
func acceptGateway(g *gateway.Gateway) {
	g.Status.Conditions = readyConditions(g.Generation, "Accepted", "Programmed")
	g.Status.Listeners = []gateway.ListenerStatus{{Name: "web", SupportedKinds: []gateway.RouteGroupKind{{Kind: "HTTPRoute"}}, Conditions: readyConditions(g.Generation, "Accepted", "Programmed", "ResolvedRefs")}}
	Expect(k8sClient.Status().Update(ctx, g)).To(Succeed())
}
func siteRoute(c *bx.BitrixCluster) *gateway.HTTPRoute {
	route := &gateway.HTTPRoute{}
	Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.RouteName(c, c.Spec.Sites[0].ID)}, route)).To(Succeed())
	return route
}
func certificateSecret(c *bx.BitrixCluster, host string, expires time.Time) *corev1.Secret {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: expires, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	Expect(err).NotTo(HaveOccurred())
	private, err := x509.MarshalPKCS8PrivateKey(key)
	Expect(err).NotTo(HaveOccurred())
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "site-tls", Namespace: c.Namespace}, Type: corev1.SecretTypeTLS, Data: map[string][]byte{corev1.TLSCertKey: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), corev1.TLSPrivateKeyKey: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})}}
}

// A failing live reader models temporary Gateway API unavailability.
type gatewayReadFailureClient struct{ client.Client }

func (r gatewayReadFailureClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*gateway.Gateway); ok {
		return errors.New("temporary Gateway observation failure")
	}
	return r.Client.Get(ctx, key, obj, opts...)
}

type gatewayReadFailure struct{ client.Reader }

func (r gatewayReadFailure) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*gateway.Gateway); ok {
		return errors.New("temporary Gateway API failure")
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

// Fail the creation of a newly desired route after an older owned route
// already exists. ResourceManager creates with UID ownership checks before SSA.
type failingRouteCreateClient struct {
	client.Client
	name string
}

func (f failingRouteCreateClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if route, ok := obj.(*unstructured.Unstructured); ok && route.GetKind() == "HTTPRoute" && route.GetName() == f.name {
		return errors.New("simulated HTTPRoute create failure")
	}
	return f.Client.Create(ctx, obj, opts...)
}

var _ = Describe("Gateway publication", func() {
	It("preserves old routes if replacement creation fails, then prunes after retry", func() {
		c := reconcileWeb(webSample())
		gatewaySample(c)
		reconcilePublished(c)
		old := siteRoute(c)
		// Replacing a site ID changes the generated route name. An API error
		// must not withdraw the existing route before its replacement exists.
		c.Spec.Sites[0].ID = "replacement"
		nextName := workload.RouteName(c, "replacement")
		failing := &BitrixClusterReconciler{
			Client: failingRouteCreateClient{Client: k8sClient, name: nextName},
			Scheme: k8sClient.Scheme(), GatewayAPIAvailable: true,
		}
		_, err := failing.reconcileGateway(ctx, c, true)
		Expect(err).To(HaveOccurred())
		previous := &gateway.HTTPRoute{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(old), previous)).To(Succeed())
		Expect(previous.UID).To(Equal(old.UID))
		replacement := &gateway.HTTPRoute{}
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKey{
			Namespace: c.Namespace, Name: nextName,
		}, replacement))).To(BeTrue())
		// A later successful retry completes replacement and only then removes
		// the obsolete route, even though the Gateway is not yet Programmed.
		healthy := &BitrixClusterReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(), GatewayAPIAvailable: true,
		}
		_, err = healthy.reconcileGateway(ctx, c, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: nextName}, replacement)).To(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(old), previous))).To(BeTrue())
	})
	It("retains the last-known-good HTTPRoute when the Gateway API fails", func() {
		c := reconcileWeb(webSample())
		gatewaySample(c)
		reconcilePublished(c)
		original := siteRoute(c)
		r := &BitrixClusterReconciler{
			Client: k8sClient, Scheme: k8sClient.Scheme(), GatewayAPIAvailable: true,
			APIReader: gatewayReadFailure{Reader: k8sClient},
		}
		_, err := r.reconcileGateway(ctx, c, true)
		Expect(err).To(HaveOccurred())
		Expect(siteRoute(c).UID).To(Equal(original.UID))
	})
	It("does not require Gateway CRDs for internal workloads", func() {
		mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: gateway.GroupName, Version: "v1"}})
		available, err := gatewayAPIsAvailable(mapper)
		Expect(err).NotTo(HaveOccurred())
		Expect(available).To(BeFalse())
		c := reconcileWeb(webSample())
		Expect(publicationReason(c)).To(Equal("Disabled"))
		gatewaySample(c)
		reconcileWeb(c)
		Expect(publicationReason(c)).To(Equal("DependencyMissing"))
		Expect(webDeployment(c)).NotTo(BeNil())
	})
	It("retains published routes when runtime reconciliation is blocked", func() {
		c := reconcileWeb(webSample())
		gatewaySample(c)
		reconcilePublished(c)
		original := siteRoute(c)
		// Simulate a temporary dependency failure without changing the desired publication.
		credentials := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: c.Spec.License.SecretRef.Name}, credentials)).To(Succeed())
		Expect(k8sClient.Delete(ctx, credentials)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("RuntimeInvalid"))
		route := siteRoute(c)
		Expect(route.UID).To(Equal(original.UID))
		Expect(route.Spec).To(Equal(original.Spec))
	})
	It("requires current route and Gateway status before publishing URLs", func() {
		c := reconcileWeb(webSample())
		g := gatewaySample(c)
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("RoutePending"))
		route := siteRoute(c)
		Expect(route.Spec.Hostnames).To(HaveLen(1))
		Expect(route.Spec.Rules[0].BackendRefs[0].Name).To(Equal(gateway.ObjectName(workload.Name(c, "web"))))
		acceptGateway(g)
		route.Status.Parents = []gateway.RouteParentStatus{{ParentRef: route.Spec.ParentRefs[0], ControllerName: "example.com/test-controller", Conditions: readyConditions(route.Generation, "Accepted", "ResolvedRefs")}}
		route.Status.Parents[0].Conditions[0].ObservedGeneration = 0
		Expect(k8sClient.Status().Update(ctx, route)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("RoutePending"))
		route.Status.Parents[0].Conditions = readyConditions(route.Generation, "Accepted", "ResolvedRefs")
		Expect(k8sClient.Status().Update(ctx, route)).To(Succeed())
		markWebReady(c)
		reconcilePublished(c)
		finishValidation(c, validationJob(c), sitesValidated, []string{"s1"})
		reconcilePublished(c)
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeTrue())
		Expect(c.Status.Sites[0].URLs).To(Equal([]string{"http://portal.example.com"}))
		version := route.ResourceVersion
		reconcilePublished(c)
		Expect(siteRoute(c).ResourceVersion).To(Equal(version))
		c.Spec.Networking.Gateway.Enabled = false
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcilePublished(c)
		routes := &gateway.HTTPRouteList{}
		Expect(k8sClient.List(ctx, routes, client.InNamespace(c.Namespace))).To(Succeed())
		Expect(routes.Items).To(BeEmpty())
		Expect(c.Status.Sites[0].URLs).To(BeEmpty())
	})

	It("never rolls a healthy Deployment on a failed Gateway observation", func() {
		c := reconcileWeb(webSample())
		g := gatewaySample(c)
		reconcilePublished(c)
		before := webDeployment(c)
		hash := before.Spec.Template.Annotations[workload.ConfigHash]
		revision := before.Spec.Template.Annotations[workload.SecretRevision]
		failing := &BitrixClusterReconciler{
			Client: gatewayReadFailureClient{Client: k8sClient},
			Scheme: k8sClient.Scheme(), GatewayAPIAvailable: true,
		}
		_, err := failing.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(c)})
		Expect(err).To(HaveOccurred())
		after := webDeployment(c)
		Expect(after.Spec.Template.Annotations[workload.ConfigHash]).To(Equal(hash))
		Expect(after.Spec.Template.Annotations[workload.SecretRevision]).To(Equal(revision))
		Expect(after.Generation).To(Equal(before.Generation))
		// An actual listener change, unlike a failed read, is desired runtime.
		g.Spec.Listeners[0].Port = 8443
		Expect(k8sClient.Update(ctx, g)).To(Succeed())
		reconcilePublished(c)
		Expect(webDeployment(c).Spec.Template.Annotations[workload.ConfigHash]).NotTo(Equal(hash))
	})
	It("withdraws routes when a listener no longer matches", func() {
		c := reconcileWeb(webSample())
		g := gatewaySample(c)
		reconcilePublished(c)
		siteRoute(c)
		// A previously published URL must not survive the withdrawal of
		// a route after a same-generation Gateway policy change.
		c.Status.Sites[0].URLs = []string{"http://portal.example.com"}
		Expect(k8sClient.Status().Update(ctx, c)).To(Succeed())
		g.Spec.Listeners[0].Hostname = ptr.To(gateway.Hostname("elsewhere.example.com"))
		Expect(k8sClient.Update(ctx, g)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("HostnameMismatch"))
		routes := &gateway.HTTPRouteList{}
		Expect(k8sClient.List(ctx, routes, client.InNamespace(c.Namespace))).To(Succeed())
		Expect(routes.Items).To(BeEmpty())
		Expect(c.Status.Sites[0].URLs).To(BeEmpty())
	})
	It("checks TLS refs, names, expiry and rotation without rolling application Secrets", func() {
		c := reconcileWeb(webSample())
		g := gatewaySample(c)
		g.Spec.Listeners[0].Protocol = gateway.HTTPSProtocolType
		g.Spec.Listeners[0].Port = 443
		g.Spec.Listeners[0].TLS = &gateway.ListenerTLSConfig{CertificateRefs: []gateway.SecretObjectReference{{Name: "site-tls"}}}
		Expect(k8sClient.Update(ctx, g)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("TLSRequired"))
		c.Spec.Sites[0].TLS = &bx.SiteTLSSpec{SecretName: "site-tls"}
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("TLSSecretMissing"))
		secret := certificateSecret(c, "wrong.example.com", time.Now().Add(time.Hour))
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("CertificateHostnameMismatch"))
		secret.Data = certificateSecret(c, "portal.example.com", time.Now().Add(-time.Minute)).Data
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("CertificateExpired"))
		secret.Data = certificateSecret(c, "portal.example.com", time.Now().Add(time.Hour)).Data
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("RoutePending"))
		siteRoute(c)
		revision := webDeployment(c).Spec.Template.Annotations[workload.SecretRevision]
		secret.Data = certificateSecret(c, "portal.example.com", time.Now().Add(2*time.Hour)).Data
		Expect(k8sClient.Update(ctx, secret)).To(Succeed())
		reconcilePublished(c)
		Expect(webDeployment(c).Spec.Template.Annotations[workload.SecretRevision]).To(Equal(revision))
	})
	It("requires allowedRoutes and a narrowly scoped cross-namespace certificate grant", func() {
		c := reconcileWeb(webSample())
		g := gatewaySample(c)
		other := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "gateway-"}}
		Expect(k8sClient.Create(ctx, other)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, other)).To(Succeed()) })
		external := g.DeepCopy()
		external.ResourceVersion = ""
		external.UID = ""
		external.Namespace = other.Name
		external.Spec.Listeners[0].Protocol = gateway.HTTPSProtocolType
		external.Spec.Listeners[0].Port = 443
		external.Spec.Listeners[0].TLS = &gateway.ListenerTLSConfig{CertificateRefs: []gateway.SecretObjectReference{{Name: "site-tls", Namespace: new(gateway.Namespace(c.Namespace))}}}
		Expect(k8sClient.Create(ctx, external)).To(Succeed())
		c.Spec.Networking.Gateway.ParentRef.Namespace = other.Name
		c.Spec.Sites[0].TLS = &bx.SiteTLSSpec{SecretName: "site-tls"}
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		Expect(k8sClient.Create(ctx, certificateSecret(c, "portal.example.com", time.Now().Add(time.Hour)))).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("RoutesNotAllowed"))
		external.Spec.Listeners[0].AllowedRoutes = &gateway.AllowedRoutes{Namespaces: &gateway.RouteNamespaces{From: ptr.To(gateway.NamespacesFromAll)}}
		Expect(k8sClient.Update(ctx, external)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("ReferenceNotPermitted"))
		grant := &gateway.ReferenceGrant{ObjectMeta: metav1.ObjectMeta{Name: "certificate", Namespace: c.Namespace}, Spec: gateway.ReferenceGrantSpec{From: []gateway.ReferenceGrantFrom{{Group: gateway.Group(gateway.GroupName), Kind: "Gateway", Namespace: gateway.Namespace(other.Name)}}, To: []gateway.ReferenceGrantTo{{Group: "", Kind: "Secret", Name: ptr.To(gateway.ObjectName("site-tls"))}}}}
		Expect(k8sClient.Create(ctx, grant)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("RoutePending"))
		Expect(k8sClient.Delete(ctx, grant)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("ReferenceNotPermitted"))
	})
	It("splits large host sets and prunes routes after site removal", func() {
		c := reconcileWeb(webSample())
		gatewaySample(c)
		c.Spec.Sites = append(c.Spec.Sites, bx.BitrixSiteSpec{ID: "s2"})
		for i := range 20 {
			c.Spec.Sites[1].Hosts = append(c.Spec.Sites[1].Hosts, fmt.Sprintf("host%d.example.com", i))
		}
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcilePublished(c)
		routes := &gateway.HTTPRouteList{}
		Expect(k8sClient.List(ctx, routes, client.InNamespace(c.Namespace))).To(Succeed())
		Expect(routes.Items).To(HaveLen(3))
		for _, route := range routes.Items {
			Expect(len(route.Spec.Hostnames)).To(BeNumerically("<=", 16))
		}
		c.Spec.Sites = c.Spec.Sites[:1]
		Expect(k8sClient.Update(ctx, c)).To(Succeed())
		reconcilePublished(c)
		Expect(k8sClient.List(ctx, routes, client.InNamespace(c.Namespace))).To(Succeed())
		Expect(routes.Items).To(HaveLen(1))
		Expect(routes.Items[0].Name).To(Equal(workload.RouteName(c, "s1")))
	})
	It("blocks both clusters claiming the same hostname and recovers after withdrawal", func() {
		c := reconcileWeb(webSample())
		g := gatewaySample(c)
		g.Spec.Listeners[0].AllowedRoutes = &gateway.AllowedRoutes{Namespaces: &gateway.RouteNamespaces{From: ptr.To(gateway.NamespacesFromAll)}}
		Expect(k8sClient.Update(ctx, g)).To(Succeed())
		other := reconcileWeb(webSample())
		other.Spec.Networking = c.Spec.Networking.DeepCopy()
		Expect(k8sClient.Update(ctx, other)).To(Succeed())
		reconcilePublished(c)
		reconcilePublished(other)
		Expect(publicationReason(c)).To(Equal("HostnameConflict"))
		Expect(publicationReason(other)).To(Equal("HostnameConflict"))
		other.Spec.Networking.Gateway.Enabled = false
		Expect(k8sClient.Update(ctx, other)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("RoutePending"))
	})

	It("reports route ownership collisions without changing healthy web status", func() {
		c := reconcileWeb(webSample())
		gatewaySample(c)
		foreign := workload.HTTPRoute(c, c.Spec.Sites[0])
		foreign.OwnerReferences = nil
		foreign.Spec.ParentRefs[0].Name = "different-gateway"
		Expect(k8sClient.Create(ctx, foreign)).To(Succeed())
		reconcilePublished(c)
		markWebReady(c)
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("ResourceCollision"))
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady")).To(BeTrue())
		Expect(meta.IsStatusConditionTrue(c.Status.Conditions, "Ready")).To(BeFalse())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
		Expect(foreign.OwnerReferences).To(BeEmpty())
	})

	It("rejects a conflicting external wildcard route without adopting it", func() {
		c := reconcileWeb(webSample())
		gatewaySample(c)
		foreign := workload.HTTPRoute(c, c.Spec.Sites[0])
		foreign.Name = "foreign"
		foreign.OwnerReferences = nil
		foreign.Spec.Hostnames = []gateway.Hostname{"*.example.com"}
		Expect(k8sClient.Create(ctx, foreign)).To(Succeed())
		reconcilePublished(c)
		Expect(publicationReason(c)).To(Equal("HostnameConflict"))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed())
		Expect(foreign.OwnerReferences).To(BeEmpty())
	})
})
