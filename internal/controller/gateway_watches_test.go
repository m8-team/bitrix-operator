package controller

import (
	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gateway "sigs.k8s.io/gateway-api/apis/v1"
)

var _ = Describe("Indexed Gateway event fanout", func() {
	It("maps GatewayClass events to only consumer clusters and scopes Namespace events", func() {
		makeCluster := func(name, namespace, gatewayName string) *bx.BitrixCluster {
			return &bx.BitrixCluster{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
				Spec: bx.BitrixClusterSpec{
					Networking: &bx.NetworkingSpec{
						Gateway: &bx.GatewaySpec{Enabled: true, ParentRef: &bx.GatewayParentReference{
							Name: gatewayName, Namespace: "ingress", SectionName: "http",
						}},
					},
				},
			}
		}
		alpha := makeCluster("alpha", "tenant-a", "edge-a")
		beta := makeCluster("beta", "tenant-b", "edge-b")
		gatewayA := &gateway.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "edge-a", Namespace: "ingress"},
			Spec: gateway.GatewaySpec{GatewayClassName: "class-a"}}
		gatewayB := &gateway.Gateway{ObjectMeta: metav1.ObjectMeta{Name: "edge-b", Namespace: "ingress"},
			Spec: gateway.GatewaySpec{GatewayClassName: "class-b"}}
		cl := fake.NewClientBuilder().
			WithScheme(k8sClient.Scheme()).
			WithObjects(alpha, beta, gatewayA, gatewayB).
			WithIndex(&bx.BitrixCluster{}, gatewayParentIndex, clusterGatewayParents).
			WithIndex(&gateway.Gateway{}, gatewayClassIndex, gatewayClassNames).
			Build()
		r := &BitrixClusterReconciler{Client: cl, GatewayParentIndexed: true}
		classRequests := r.gatewayRequests(ctx, &gateway.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "class-a"}})
		Expect(classRequests).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(alpha)}))
		ns := &metav1.PartialObjectMetadata{ObjectMeta: metav1.ObjectMeta{Name: "tenant-b"}}
		nsRequests := r.gatewayRequests(ctx, ns)
		Expect(nsRequests).To(ConsistOf(ctrl.Request{NamespacedName: client.ObjectKeyFromObject(beta)}))
		unknown := &gateway.GatewayClass{ObjectMeta: metav1.ObjectMeta{Name: "unknown-class"}}
		Expect(r.gatewayRequests(ctx, unknown)).To(BeEmpty())
	})
})
