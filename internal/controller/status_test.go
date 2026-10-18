package controller

import (
	"testing"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
