package controller

import (
	"context"
	"errors"
	"reflect"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func condition(c *bx.BitrixCluster, kind string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{Type: kind, Status: status, Reason: reason, Message: message, ObservedGeneration: c.Generation})
}

func initializeReconcileStatus(c *bx.BitrixCluster) {
	c.Status.ObservedGeneration = c.Generation
	c.Status.Phase = bx.BitrixClusterPhaseProgressing
	c.Status.Release = nil
	c.Status.Runtime = &bx.RuntimeStatus{Web: bx.WorkloadStatus{DesiredReplicas: c.Spec.Runtime.Web.Replicas}}
	c.Status.Sites = nil
	for _, site := range c.Spec.Sites {
		c.Status.Sites = append(c.Status.Sites, bx.BitrixSiteStatus{ID: site.ID, Message: "Site existence has not been validated through the Bitrix kernel"})
	}
	condition(c, "Ready", metav1.ConditionFalse, "SiteValidationPending", "Web readiness does not validate Bitrix site IDs; kernel validation is pending")
	condition(c, conditionSitesValidated, metav1.ConditionUnknown, "ValidationPending", "Waiting for site validation of the current runtime")
	condition(c, conditionWebReady, metav1.ConditionFalse, "RolloutPending", "Waiting for the desired web rollout")
	condition(c, "CronReady", metav1.ConditionTrue, "Disabled", "Cron workload is disabled")
	if c.Spec.Runtime.Cron.Enabled {
		c.Status.Runtime.Cron.DesiredReplicas = 1
		condition(c, "CronReady", metav1.ConditionFalse, "RolloutPending", "Waiting for the desired cron rollout")
	}
	condition(c, "AutoscalingReady", metav1.ConditionTrue, "Disabled", "Web autoscaling is disabled")
	if workload.AutoscalingEnabled(c) {
		condition(c, "AutoscalingReady", metav1.ConditionFalse, "AutoscalingPending", "Waiting for the HPA")
	}
	initializePushStatus(c)
	condition(c, "GatewayReady", metav1.ConditionTrue, "Disabled", "Gateway publication is disabled")
	if gatewayEnabled(c) {
		condition(c, "GatewayReady", metav1.ConditionFalse, "PublicationPending", "Waiting for Gateway publication")
	}
	condition(c, "StorageReady", metav1.ConditionUnknown, "NotObserved", "Uploads storage has not been observed")
}

// resolveReconcileResult is pure and owns the final readiness/error policy.
// A failure in a later stage must never rewrite an already observed WebReady,
// CronReady, or PushReady condition from an unrelated component.
func resolveReconcileResult(c *bx.BitrixCluster, resourceErr, gatewayErr error) (ctrl.Result, error) {
	runtimeIssue := classifyReconcileError(resourceErr, false)
	publicationIssue := classifyReconcileError(gatewayErr, true)

	if gatewayErr != nil {
		condition(c, "GatewayReady", metav1.ConditionFalse, publicationIssue.reason, publicationIssue.message)
	}
	if resourceErr != nil {
		condition(c, "Ready", metav1.ConditionFalse, runtimeIssue.reason, runtimeIssue.message)
		attributeComponentFailure(c, resourceErr, runtimeIssue)
	} else {
		summarizeReadiness(c)
	}
	if (resourceErr != nil && runtimeIssue.disposition != dispositionWaiting) ||
		(gatewayErr != nil && publicationIssue.disposition != dispositionWaiting) {
		c.Status.Phase = bx.BitrixClusterPhaseDegraded
	}

	// The controller-runtime workqueue ignores RequeueAfter when err != nil.
	// Returning an empty Result here makes that retry behavior explicit.
	if runtimeIssue.disposition == dispositionTransient || publicationIssue.disposition == dispositionTransient {
		return ctrl.Result{}, errors.Join(runtimeIssue.cause, publicationIssue.cause)
	}

	retry := 30 * time.Second
	if resourceErr != nil {
		retry = runtimeIssue.retryAfter
	}
	if gatewayErr != nil {
		retry = shorterRequeue(retry, publicationIssue.retryAfter)
	}
	return ctrl.Result{RequeueAfter: retry}, nil
}

// Attribute a failure only to the stage that failed. In particular, a GC or
// validation error is not evidence that an observed web rollout stopped.
func attributeComponentFailure(c *bx.BitrixCluster, err error, issue reconcileFailure) {
	stage := ""
	for _, name := range []string{stagePush, stageWeb, stageCron, stageWebStatus, stageSites} {
		if failedComponent(err, name) {
			stage = name
			break
		}
	}
	kind := ""
	switch stage {
	case stagePush:
		kind = "PushReady"
	case stageWeb:
		kind = conditionWebReady
	case stageCron:
		kind = "CronReady"
	case stageSites:
		kind = conditionSitesValidated
	case stageWebStatus:
		kind = conditionWebReady
		if meta.IsStatusConditionTrue(c.Status.Conditions, conditionWebReady) && workload.AutoscalingEnabled(c) {
			kind = "AutoscalingReady"
		}
	}
	if kind == "" && failedComponent(err, "prepare") {
		// Preparation also performs safe disabling before validating Secrets.
		// Report a stopping controller even when the desired spec disables it.
		switch issue.reason {
		case "PushStopping":
			kind = "PushReady"
		case "CronStopping":
			kind = "CronReady"
		case "SecretKeyInvalid":
			if pushEnabled(c) {
				kind = "PushReady"
			}
		}
	}
	if kind == "" {
		return
	}
	if existing := meta.FindStatusCondition(c.Status.Conditions, kind); existing != nil && existing.Status == metav1.ConditionTrue && !failedComponent(err, "prepare") {
		return
	}
	condition(c, kind, metav1.ConditionFalse, issue.reason, issue.message)
}

func preserveConditionTransitions(before, c *bx.BitrixCluster) {
	for i := range c.Status.Conditions {
		previous := meta.FindStatusCondition(before.Status.Conditions, c.Status.Conditions[i].Type)
		if previous != nil && previous.Status == c.Status.Conditions[i].Status {
			c.Status.Conditions[i].LastTransitionTime = previous.LastTransitionTime
		}
	}
}

// Only this method writes the BitrixCluster status. Optimistic locking prevents
// overwriting concurrent user or controller changes; events follow persistence.
func (r *BitrixClusterReconciler) persistReconcileStatus(ctx context.Context, before, c *bx.BitrixCluster) error {
	if reflect.DeepEqual(before.Status, c.Status) {
		return nil
	}
	if err := r.Status().Patch(ctx, c, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
		return err
	}
	r.recordTransition(before, c)
	return nil
}
