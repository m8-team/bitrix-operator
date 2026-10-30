package controller

import (
	"errors"
	"hash/fnv"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	"github.com/m8-team/bitrix-operator/internal/workload"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func condition(c *bx.BitrixCluster, kind string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{Type: kind, Status: status, Reason: reason, Message: message, ObservedGeneration: c.Generation})
}

func initializeReconcileStatus(c *bx.BitrixCluster) {
	// A retry of the same spec generation must not erase independent workload
	// observations before prepare/config/Push can run. Any failure in those
	// stages still marks the overall Ready condition as false.
	if c.Status.ObservedGeneration == c.Generation && c.Status.Runtime != nil {
		c.Status.Phase = bx.BitrixClusterPhaseProgressing
		return
	}
	// A different generation cannot inherit readiness from an older desired spec.
	// Until a workload is actually observed, Unknown is more accurate than
	// inventing an in-progress rollout.
	c.Status.ObservedGeneration = c.Generation
	c.Status.Phase = bx.BitrixClusterPhaseProgressing
	c.Status.Release = nil
	c.Status.SiteValidation = nil
	c.Status.Runtime = &bx.RuntimeStatus{Web: bx.WorkloadStatus{DesiredReplicas: c.Spec.Runtime.Web.Replicas}}
	c.Status.Sites = nil
	for _, site := range c.Spec.Sites {
		c.Status.Sites = append(c.Status.Sites, bx.BitrixSiteStatus{ID: site.ID, Message: "Site existence has not been validated through the Bitrix kernel"})
	}
	condition(c, "Ready", metav1.ConditionFalse, "SiteValidationPending", "Web readiness does not validate Bitrix site IDs; kernel validation is pending")
	condition(c, conditionSitesValidated, metav1.ConditionUnknown, "ValidationPending", "Waiting for site validation of the current runtime")
	condition(c, conditionWebReady, metav1.ConditionUnknown, "NotObserved", "The web Deployment has not yet been observed for this generation")
	condition(c, conditionCronReady, metav1.ConditionTrue, "Disabled", "Cron workload is disabled")
	if c.Spec.Runtime.Cron.Enabled {
		c.Status.Runtime.Cron.DesiredReplicas = 1
		condition(c, conditionCronReady, metav1.ConditionUnknown, "NotObserved", "The cron Deployment has not yet been observed for this generation")
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

const (
	pendingRequeueAfter         = 30 * time.Second
	healthyRequeueAfter         = 10 * time.Minute
	healthyRequeueJitterSeconds = 120
)

// healthyRequeueInterval bounds the watch-driven model with periodic drift
// detection, spreading healthy clusters over two minutes to avoid synchronized
// full resyncs after a controller rollout.
func healthyRequeueInterval(c *bx.BitrixCluster) time.Duration {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(c.UID))
	return healthyRequeueAfter + time.Duration(hash.Sum32()%healthyRequeueJitterSeconds)*time.Second
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

	retry := pendingRequeueAfter
	if resourceErr == nil && gatewayErr == nil && meta.IsStatusConditionTrue(c.Status.Conditions, "Ready") {
		retry = healthyRequeueInterval(c)
		if proof := c.Status.SiteValidation; proof != nil {
			// Wake up at the proof TTL, not as much as 12 minutes afterwards.
			// During refresh also honor the maximum staleness deadline.
			now := time.Now()
			next := proof.ValidatedAt.Add(siteValidationTTL)
			if !now.Before(next) {
				next = proof.ValidatedAt.Add(siteValidationMaxStaleness)
			}
			if next.After(now) && next.Sub(now) < retry {
				retry = next.Sub(now)
			}
		}
	}
	if resourceErr != nil {
		retry = runtimeIssue.retryAfter
	}
	if gatewayErr != nil {
		if resourceErr == nil {
			retry = publicationIssue.retryAfter
		} else {
			retry = shorterRequeue(retry, publicationIssue.retryAfter)
		}
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
		kind = conditionPushReady
	case stageWeb:
		kind = conditionWebReady
	case stageCron:
		kind = conditionCronReady
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
			kind = conditionPushReady
		case "CronStopping":
			kind = conditionCronReady
		case "SecretKeyInvalid":
			if pushEnabled(c) {
				kind = conditionPushReady
			}
		}
	}
	if kind == "" {
		return
	}
	if existing := meta.FindStatusCondition(c.Status.Conditions, kind); existing != nil && existing.Status == metav1.ConditionUnknown && existing.Reason == "ObservationFailed" {
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
