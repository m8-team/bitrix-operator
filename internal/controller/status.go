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
	condition(c, "SitesValidated", metav1.ConditionUnknown, "ValidationPending", "Waiting for site validation of the current runtime")
	condition(c, "WebReady", metav1.ConditionFalse, "RolloutPending", "Waiting for the desired web rollout")
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

// resolveReconcileResult is pure: no API calls, client state or status writes.
// Unit tests can exercise error policy and Conditions independently of Kubernetes.
func resolveReconcileResult(c *bx.BitrixCluster, resourceErr, gatewayErr error) (ctrl.Result, error) {
	result := ctrl.Result{RequeueAfter: 30 * time.Second}
	var gatewayRetry time.Duration
	if gatewayErr != nil {
		issue := classifyReconcileError(gatewayErr, true)
		condition(c, "GatewayReady", metav1.ConditionFalse, issue.reason, issue.message)
		gatewayRetry = issue.retryAfter
		gatewayErr = issue.cause
	}
	if resourceErr == nil {
		summarizeReadiness(c)
	} else {
		issue := classifyReconcileError(resourceErr, false)
		result.RequeueAfter = issue.retryAfter
		c.Status.Phase = bx.BitrixClusterPhaseDegraded
		condition(c, "Ready", metav1.ConditionFalse, issue.reason, issue.message)
		condition(c, "WebReady", metav1.ConditionFalse, issue.reason, issue.message)
		if pushEnabled(c) || issue.reason == "PushStopping" {
			condition(c, "PushReady", metav1.ConditionFalse, issue.reason, issue.message)
		}
		if c.Spec.Runtime.Cron.Enabled || issue.reason == "CronStopping" {
			condition(c, "CronReady", metav1.ConditionFalse, issue.reason, issue.message)
		}
		resourceErr = issue.cause
	}
	result.RequeueAfter = shorterRequeue(result.RequeueAfter, gatewayRetry)
	return result, errors.Join(resourceErr, gatewayErr)
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
