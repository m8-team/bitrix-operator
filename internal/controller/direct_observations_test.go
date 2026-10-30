package controller

import (
	"reflect"
	"testing"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
