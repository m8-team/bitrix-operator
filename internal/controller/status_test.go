package controller

import (
	"errors"
	"testing"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

func TestStatusInitializationWithoutKubernetes(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 7}}
	c.Spec.Sites = []bx.BitrixSiteSpec{{ID: "store"}}
	c.Spec.Runtime.Web.Replicas = 3
	initializeReconcileStatus(c)
	if c.Status.ObservedGeneration != 7 || c.Status.Runtime.Web.DesiredReplicas != 3 ||
		len(c.Status.Sites) != 1 || c.Status.Sites[0].ID != "store" {
		t.Fatalf("incorrect initial status: %+v", c.Status)
	}
	if cond := meta.FindStatusCondition(c.Status.Conditions, "SitesValidated"); cond == nil ||
		cond.Status != metav1.ConditionUnknown || cond.ObservedGeneration != 7 {
		t.Fatalf("missing current validation condition: %+v", cond)
	}
}

func TestStatusErrorPolicyWithoutKubernetes(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 9}}
	initializeReconcileStatus(c)
	result, err := resolveReconcileResult(c, block("SecretMissing", "credential missing"), nil)
	if err != nil || result.RequeueAfter != time.Minute {
		t.Fatalf("blocked retry: result=%+v err=%v", result, err)
	}
	if ready := meta.FindStatusCondition(c.Status.Conditions, "Ready"); ready == nil || ready.Reason != "SecretMissing" ||
		c.Status.Phase != bx.BitrixClusterPhaseDegraded {
		t.Fatalf("incorrect blocked status: %+v", c.Status)
	}
}

func TestStatusTransitionTimesAreStable(t *testing.T) {
	before := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 12}}
	before.Status.Conditions = []metav1.Condition{{
		Type: "Ready", Status: metav1.ConditionFalse, Reason: "RolloutPending",
		Message: "pending", ObservedGeneration: 11,
		LastTransitionTime: metav1.NewTime(time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)),
	}}
	after := before.DeepCopy()
	condition(after, "Ready", metav1.ConditionFalse, "SecretMissing", "secret missing")
	preserveConditionTransitions(before, after)
	if got := meta.FindStatusCondition(after.Status.Conditions, "Ready"); got.LastTransitionTime != before.Status.Conditions[0].LastTransitionTime {
		t.Fatal("a reason-only change should not reset the status transition time")
	}
	condition(after, "Ready", metav1.ConditionTrue, "ComponentsReady", "ok")
	preserveConditionTransitions(before, after)
	if got := meta.FindStatusCondition(after.Status.Conditions, "Ready"); got.LastTransitionTime == before.Status.Conditions[0].LastTransitionTime {
		t.Fatal("a status change must retain its new transition time")
	}
}

func TestUnrelatedStageFailurePreservesObservedConditions(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 3}}
	initializeReconcileStatus(c)
	condition(c, "WebReady", metav1.ConditionTrue, "RolloutComplete", "web ready")
	condition(c, "SitesValidated", metav1.ConditionTrue, "Validated", "site validated")
	condition(c, "StorageReady", metav1.ConditionTrue, "VolumeBound", "storage bound")
	gcErr := errors.New("failed to inspect configmap references")
	result, err := resolveReconcileResult(c, &componentError{name: "config-gc", cause: gcErr}, nil)
	if !errors.Is(err, gcErr) || result.RequeueAfter != 0 {
		t.Fatalf("transient error must use rate-limited queue: result=%+v err=%v", result, err)
	}
	for _, kind := range []string{"WebReady", "SitesValidated", "StorageReady"} {
		if !meta.IsStatusConditionTrue(c.Status.Conditions, kind) {
			t.Fatalf("unrelated %s was cleared: %+v", kind, c.Status.Conditions)
		}
	}
	if meta.IsStatusConditionTrue(c.Status.Conditions, "Ready") ||
		c.Status.Phase != bx.BitrixClusterPhaseDegraded ||
		meta.FindStatusCondition(c.Status.Conditions, "Ready").Reason != "ReconcileFailed" {
		t.Fatalf("overall failure missing after GC error: %+v", c.Status)
	}
}

func TestSiteFailureDoesNotInvalidateWebRollout(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 8}}
	initializeReconcileStatus(c)
	condition(c, "WebReady", metav1.ConditionTrue, "RolloutComplete", "web ready")
	_, err := resolveReconcileResult(c, &componentError{name: "sites", cause: block("ValidationUnavailable", "validation failed")}, nil)
	if err != nil {
		t.Fatalf("blocked validation should not be a rate-limited error: %v", err)
	}
	if !meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady") {
		t.Fatalf("site error cleared web readiness: %+v", c.Status.Conditions)
	}
	if site := meta.FindStatusCondition(c.Status.Conditions, "SitesValidated"); site.Status != metav1.ConditionFalse || site.Reason != "ValidationUnavailable" {
		t.Fatalf("expected attributed site failure, got %+v", site)
	}
}

func TestGatewayFailureIsIndependentOfRuntime(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 4}}
	initializeReconcileStatus(c)
	condition(c, "WebReady", metav1.ConditionTrue, "RolloutComplete", "web ready")
	gatewayErr := errors.New("gateway unavailable")
	result, err := resolveReconcileResult(c, nil, gatewayErr)
	if !errors.Is(err, gatewayErr) || result.RequeueAfter != 0 {
		t.Fatalf("gateway error should use rate limiter: result=%+v err=%v", result, err)
	}
	if !meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady") {
		t.Fatalf("gateway error must not reset web readiness: %+v", c.Status.Conditions)
	}
	if gw := meta.FindStatusCondition(c.Status.Conditions, "GatewayReady"); gw == nil || gw.Reason != "PublicationFailed" {
		t.Fatalf("missing Gateway error status: %+v", gw)
	}
}

func TestConflictIsWaitingNotTransient(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 4}}
	initializeReconcileStatus(c)
	conflict := apierrors.NewConflict(schema.GroupResource{Resource: "deployments"}, "web", errors.New("conflict"))
	result, err := resolveReconcileResult(c, &componentError{name: "web", cause: conflict}, nil)
	if err != nil || result.RequeueAfter != 10*time.Second {
		t.Fatalf("conflict should be scheduled, not rate limited: result=%+v err=%v", result, err)
	}
	if c.Status.Phase != bx.BitrixClusterPhaseProgressing {
		t.Fatalf("expected progressing phase on conflict, got %s", c.Status.Phase)
	}
	if web := meta.FindStatusCondition(c.Status.Conditions, "WebReady"); web.Reason != "ApplyConflict" {
		t.Fatalf("missing component-specific conflict: %+v", web)
	}
}

func TestHealthyRequeueIsBoundedAndDeterministicallyStaggered(t *testing.T) {
	makeReady := func(uid string) *bx.BitrixCluster {
		c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 2, UID: types.UID(uid)}}
		initializeReconcileStatus(c)
		for _, kind := range []string{"StorageReady", conditionWebReady, "CronReady", "PushReady", conditionSitesValidated, "GatewayReady", "AutoscalingReady"} {
			condition(c, kind, metav1.ConditionTrue, "Verified", "verified")
		}
		return c
	}
	first := makeReady("cluster-one")
	result, err := resolveReconcileResult(first, nil, nil)
	if err != nil || first.Status.Phase != bx.BitrixClusterPhaseReady {
		t.Fatalf("fully observed cluster must be ready: %+v, %v", first.Status, err)
	}
	if result.RequeueAfter < healthyRequeueAfter || result.RequeueAfter >= healthyRequeueAfter+healthyRequeueJitterSeconds*time.Second {
		t.Fatalf("healthy interval not bounded: %s", result.RequeueAfter)
	}
	if second := healthyRequeueInterval(makeReady("cluster-one")); result.RequeueAfter != second {
		t.Fatalf("healthy interval must be stable per UID: %s != %s", result.RequeueAfter, second)
	}
	if another := healthyRequeueInterval(makeReady("cluster-two")); another == result.RequeueAfter {
		t.Fatalf("different cluster UIDs unexpectedly share the same resync delay: %s", another)
	}
}

func TestPendingRequeueRemainsFast(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 9, UID: types.UID("pending")}}
	initializeReconcileStatus(c)
	result, err := resolveReconcileResult(c, nil, nil)
	if err != nil || result.RequeueAfter != pendingRequeueAfter {
		t.Fatalf("pending components must retain fast safety checks: %+v, %v", result, err)
	}
}

func TestEarlyFailureKeepsObservedWorkloadsForSameGeneration(t *testing.T) {
	c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 7}}
	c.Spec.Runtime.Web.Replicas = 3
	c.Spec.Runtime.Cron.Enabled = true
	c.Spec.Sites = []bx.BitrixSiteSpec{{ID: "s1", Hosts: []string{"example.test"}}}
	initializeReconcileStatus(c)
	c.Status.Runtime.Web.ReadyReplicas = 3
	c.Status.Runtime.Cron.ReadyReplicas = 1
	c.Status.Release = &bx.ReleaseStatus{Tag: "stable", Digest: "sha256:old"}
	condition(c, conditionWebReady, metav1.ConditionTrue, "RolloutComplete", "web observed")
	condition(c, conditionCronReady, metav1.ConditionTrue, "RolloutComplete", "cron observed")
	condition(c, conditionSitesValidated, metav1.ConditionTrue, "Validated", "validated")
	initializeReconcileStatus(c)
	_, err := resolveReconcileResult(c, &componentError{name: "prepare", cause: block("SecretMissing", "license Secret missing")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status.Runtime.Web.ReadyReplicas != 3 || c.Status.Runtime.Cron.ReadyReplicas != 1 || c.Status.Release == nil ||
		!meta.IsStatusConditionTrue(c.Status.Conditions, conditionWebReady) || !meta.IsStatusConditionTrue(c.Status.Conditions, conditionCronReady) {
		t.Fatalf("early failure erased observations: %+v", c.Status)
	}
	if meta.IsStatusConditionTrue(c.Status.Conditions, "Ready") || c.Status.Phase != bx.BitrixClusterPhaseDegraded {
		t.Fatalf("early blocked preparation must keep overall Ready false: %+v", c.Status)
	}
	c.Generation++
	initializeReconcileStatus(c)
	if c.Status.Runtime.Web.ReadyReplicas != 0 || c.Status.Release != nil {
		t.Fatalf("new generation inherited old runtime observations: %+v", c.Status)
	}
	for _, kind := range []string{conditionWebReady, conditionCronReady, conditionSitesValidated} {
		observed := meta.FindStatusCondition(c.Status.Conditions, kind)
		if observed == nil || observed.Status != metav1.ConditionUnknown || observed.ObservedGeneration != c.Generation {
			t.Fatalf("new generation must not claim verified %s: %+v", kind, observed)
		}
	}
}

func TestGatewayBlockedErrorDegradesAndWaitingRemainsProgressing(t *testing.T) {
	for _, tt := range []struct {
		name string
		err error
		phase bx.BitrixClusterPhase
		requeue time.Duration
	}{
		{"invalid publication", block("InvalidGateway", "bad listener"), bx.BitrixClusterPhaseDegraded, time.Minute},
		{"expected stopping", waitFor("CronStopping", "waiting for Pod deletion", 30*time.Second), bx.BitrixClusterPhaseProgressing, 30*time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &bx.BitrixCluster{ObjectMeta: metav1.ObjectMeta{Generation: 2}}
			initializeReconcileStatus(c)
			var resultTime time.Duration
			var err error
			if tt.name == "invalid publication" {
				result, e := resolveReconcileResult(c, nil, tt.err)
				resultTime, err = result.RequeueAfter, e
				gw := meta.FindStatusCondition(c.Status.Conditions, "GatewayReady")
				if gw == nil || gw.Reason != "InvalidGateway" {
					t.Fatalf("lost blocked Gateway reason: %+v", gw)
				}
			} else {
				result, e := resolveReconcileResult(c, tt.err, nil)
				resultTime, err = result.RequeueAfter, e
			}
			if err != nil || c.Status.Phase != tt.phase || resultTime != tt.requeue {
				t.Fatalf("wrong disposition: phase=%s retry=%s err=%v", c.Status.Phase, resultTime, err)
			}
		})
	}
}
