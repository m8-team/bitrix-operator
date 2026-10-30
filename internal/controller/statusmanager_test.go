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

func TestStatusManagerApplyDetachesComponentObservation(t *testing.T) {
	cluster := &bx.BitrixCluster{}
	result := ComponentResult{
		Name:                  "sites",
		RuntimeChanged:        true,
		Runtime:               &bx.RuntimeStatus{Web: bx.WorkloadStatus{ReadyReplicas: 3}},
		ReleaseChanged:        true,
		Release:               &bx.ReleaseStatus{Digest: "sha256:original"},
		SitesChanged:          true,
		Sites:                 []bx.BitrixSiteStatus{{ID: "store", URLs: []string{"https://store.example.test"}, Exists: true}},
		SiteValidationChanged: true,
		SiteValidation:        &bx.SiteValidationStatus{Identity: "observed-release"},
	}
	(&StatusManager{cluster: cluster}).Apply(result)
	result.Runtime.Web.ReadyReplicas = 0
	result.Release.Digest = "sha256:changed"
	result.Sites[0].URLs[0] = "https://untrusted.example.test"
	result.Sites[0].Exists = false
	result.SiteValidation.Identity = "changed-release"
	if cluster.Status.Runtime.Web.ReadyReplicas != 3 ||
		cluster.Status.Release.Digest != "sha256:original" ||
		cluster.Status.Sites[0].URLs[0] != "https://store.example.test" ||
		!cluster.Status.Sites[0].Exists ||
		cluster.Status.SiteValidation.Identity != "observed-release" {
		t.Fatalf("applied status aliases the component result: %+v", cluster.Status)
	}
	cluster.Status.Sites[0].URLs[0] = "https://cluster.example.test"
	if result.Sites[0].URLs[0] != "https://untrusted.example.test" {
		t.Fatal("mutating status changed a component's isolated result")
	}
}

func TestStatusManagerApplyHonorsNilAndUnchangedFields(t *testing.T) {
	cluster := &bx.BitrixCluster{}
	cluster.Status.Runtime = &bx.RuntimeStatus{Web: bx.WorkloadStatus{ReadyReplicas: 2}}
	cluster.Status.Release = &bx.ReleaseStatus{Digest: "sha256:original"}
	cluster.Status.Sites = []bx.BitrixSiteStatus{{ID: "old"}}
	cluster.Status.SiteValidation = &bx.SiteValidationStatus{Identity: "old"}
	manager := &StatusManager{cluster: cluster}
	manager.Apply(ComponentResult{})
	if cluster.Status.Runtime == nil || cluster.Status.Release == nil ||
		len(cluster.Status.Sites) != 1 || cluster.Status.SiteValidation == nil {
		t.Fatal("unchanged fields were reset by an empty observation")
	}
	manager.Apply(ComponentResult{
		RuntimeChanged: true, ReleaseChanged: true,
		SitesChanged: true, SiteValidationChanged: true,
	})
	if cluster.Status.Runtime != nil || cluster.Status.Release != nil ||
		cluster.Status.Sites != nil || cluster.Status.SiteValidation != nil {
		t.Fatalf("explicitly cleared status fields were not removed: %+v", cluster.Status)
	}
}
