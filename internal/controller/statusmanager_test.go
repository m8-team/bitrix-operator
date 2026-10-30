package controller

import (
	"context"
	"testing"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestStatusManagerOwnsStatusPersistence(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	if err := bx.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	original := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Name: "portal", Namespace: "tenant", Generation: 3}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&bx.BitrixCluster{}).WithObjects(original).Build()
	current := &bx.BitrixCluster{}
	if err := kube.Get(ctx, client.ObjectKeyFromObject(original), current); err != nil {
		t.Fatal(err)
	}
	manager := NewStatusManager(&BitrixClusterReconciler{Client: kube}, current)
	manager.Begin()
	manager.SetCondition("WebReady", metav1.ConditionTrue, "Observed", "verified")
	if err := manager.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	observed := &bx.BitrixCluster{}
	if err := kube.Get(ctx, client.ObjectKeyFromObject(original), observed); err != nil {
		t.Fatal(err)
	}
	if observed.Status.ObservedGeneration != 3 || !meta.IsStatusConditionTrue(observed.Status.Conditions, "WebReady") {
		t.Fatalf("status manager did not persist Conditions: %+v", observed.Status)
	}
	if observed.Generation != original.Generation {
		t.Fatalf("status manager modified generation: %d", observed.Generation)
	}
}
