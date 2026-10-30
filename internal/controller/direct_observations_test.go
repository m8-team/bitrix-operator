package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDirectSiteValidationExpiresProofWithoutMutatingSharedStatus(t *testing.T) {
	now := time.Now()
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 12}}
	c.Status.Sites = []bx.BitrixSiteStatus{{ID: "portal", Exists: true, URLs: []string{"https://example.test"}}}
	c.Status.SiteValidation = &bx.SiteValidationStatus{
		Identity: "current", JobUID: "job", ValidatedAt: metav1.NewTime(now.Add(-31 * time.Minute)),
	}
	condition(c, conditionSitesValidated, metav1.ConditionTrue, sitesValidated, "validated")
	original := c.Status.DeepCopy()
	observation := ComponentResult{}
	proof := siteProof(c, &observation, "current", now)
	if proof == nil {
		t.Fatal("existing matching proof should be supplied to expiry policy")
	}
	retainSiteEvidence(c, &observation, proof, now)
	if !reflect.DeepEqual(c.Status, *original) {
		t.Fatal("site observation mutated the shared BitrixCluster status")
	}
	(&StatusManager{cluster: c}).Apply(observation)
	validation := meta.FindStatusCondition(c.Status.Conditions, conditionSitesValidated)
	if validation == nil || validation.Status != metav1.ConditionUnknown || validation.Reason != "ValidationStale" {
		t.Fatalf("expired validation should be unknown: %+v", validation)
	}
	if c.Status.Sites[0].Exists || c.Status.Sites[0].URLs[0] != "https://example.test" {
		t.Fatalf("expiry should invalidate existence without forging a publication change: %+v", c.Status.Sites)
	}
}

func TestDirectSiteValidationRejectsDifferentIdentity(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 7}}
	c.Status.SiteValidation = &bx.SiteValidationStatus{
		Identity: "old-release", JobUID: "old-job", ValidatedAt: metav1.Now(),
	}
	observation := ComponentResult{}
	if proof := siteProof(c, &observation, "new-release", time.Now()); proof != nil {
		t.Fatalf("proof from a different release was accepted: %+v", proof)
	}
	if !observation.SiteValidationChanged || observation.SiteValidation != nil {
		t.Fatalf("changed identity must revoke proof in the result: %+v", observation)
	}
	if c.Status.SiteValidation == nil {
		t.Fatal("shared proof was changed before StatusManager.Apply")
	}
	(&StatusManager{cluster: c}).Apply(observation)
	if c.Status.SiteValidation != nil {
		t.Fatal("stale release proof survived status aggregation")
	}
}

func TestGatewayURLRemovalIsAnIsolatedObservation(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 3}}
	c.Status.Sites = []bx.BitrixSiteStatus{{ID: "store", Exists: true, URLs: []string{"https://shop.example.test"}}}
	original := c.Status.DeepCopy()
	result := ComponentResult{}
	clearPublishedSiteURLs(c, &result)
	if !result.SitesChanged || !reflect.DeepEqual(c.Status, *original) {
		t.Fatalf("gateway URL observation bypassed status isolation: %+v", result)
	}
	(&StatusManager{cluster: c}).Apply(result)
	if len(c.Status.Sites[0].URLs) != 0 || !c.Status.Sites[0].Exists {
		t.Fatalf("URL withdrawal changed site existence or failed to clear URLs: %+v", c.Status.Sites)
	}
}

func TestPreparationPreservesSuccessfulCleanupWhenValidationFails(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, register := range []func(*runtime.Scheme) error{
		appsv1.AddToScheme, autoscalingv2.AddToScheme, corev1.AddToScheme, networkingv1.AddToScheme,
	} {
		if err := register(scheme); err != nil {
			t.Fatal(err)
		}
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &BitrixClusterReconciler{Client: kube}
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Name: "portal", Namespace: "tenant", Generation: 5}}
	initializeReconcileStatus(c)
	before := c.Status.DeepCopy()
	_, observation, err := r.prepareRuntime(context.Background(), c)
	if err == nil {
		t.Fatal("empty storage configuration should fail validation")
	}
	if !reflect.DeepEqual(c.Status, *before) {
		t.Fatal("preparation bypassed StatusManager and changed shared status")
	}
	(&StatusManager{cluster: c}).Apply(observation)
	for _, kind := range []string{"AutoscalingReady", conditionCronReady, conditionPushReady} {
		observed := meta.FindStatusCondition(c.Status.Conditions, kind)
		if observed == nil || observed.Status != metav1.ConditionTrue || observed.Reason != "Disabled" {
			t.Fatalf("successful cleanup %s lost after invalid storage: %+v", kind, observed)
		}
	}
	if c.Status.Runtime.Cron.ReadyReplicas != 0 || c.Status.Runtime.PushPublisher.ReadyReplicas != 0 {
		t.Fatalf("cleanup failed to clear old disabled workload observations: %+v", c.Status.Runtime)
	}
}

func TestPreparationFailureIsAttributedAndRetainsCleanupConditions(t *testing.T) {
	scheme := runtime.NewScheme()
	for _, register := range []func(*runtime.Scheme) error{
		appsv1.AddToScheme, autoscalingv2.AddToScheme, corev1.AddToScheme, networkingv1.AddToScheme,
	} {
		if err := register(scheme); err != nil {
			t.Fatal(err)
		}
	}
	r := &BitrixClusterReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Name: "portal", Namespace: "tenant", Generation: 8}}
	status := NewStatusManager(r, c)
	status.Begin()
	err := r.reconcileResources(context.Background(), status)
	var failed *componentError
	if !errors.As(err, &failed) || failed.name != stagePrepare {
		t.Fatalf("preparation failure was not attributed: %v", err)
	}
	for _, kind := range []string{"AutoscalingReady", conditionCronReady, conditionPushReady} {
		got := meta.FindStatusCondition(c.Status.Conditions, kind)
		if got == nil || got.Status != metav1.ConditionTrue || got.Reason != "Disabled" {
			t.Fatalf("cleanup observation lost after preparation failure: %s: %+v", kind, got)
		}
	}
}
