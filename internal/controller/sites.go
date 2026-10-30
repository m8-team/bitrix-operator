package controller

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	sitesValidated             = "Validated"
	siteValidationTTL          = 15 * time.Minute
	siteValidationMaxStaleness = 30 * time.Minute
)

var imageDigest = regexp.MustCompile(`(?:^|@|://)(sha256:[a-f0-9]{64})$`)

func releaseDigest(p *corev1.Pod) string {
	for _, s := range p.Status.InitContainerStatuses {
		if s.Name == "release" && s.State.Terminated != nil && s.State.Terminated.ExitCode == 0 {
			if matches := imageDigest.FindStringSubmatch(s.ImageID); len(matches) == 2 {
				return matches[1]
			}
		}
	}
	return ""
}

func containerDigest(p *corev1.Pod, name string) string {
	for _, s := range p.Status.ContainerStatuses {
		if s.Name == name {
			if matches := imageDigest.FindStringSubmatch(s.ImageID); len(matches) == 2 {
				return matches[1]
			}
		}
	}
	return ""
}

// Resolve the actual release and PHP runtime images, including tag-only references. Labels are hints;
// the Deployment -> ReplicaSet -> Pod ownership chain is checked before using status.
func (r *BitrixClusterReconciler) observedRuntimeDigests(ctx context.Context, c *bx.BitrixCluster, b *render.Bundle, revision, image string) (string, string, error) {
	pods := &corev1.PodList{}
	if err := r.observationReader().List(ctx, pods, client.InNamespace(c.Namespace), client.MatchingLabels{workload.OwnerUID: string(c.UID), "app.kubernetes.io/name": "bitrix"}); err != nil {
		return "", "", err
	}
	deployment := &appsv1.Deployment{}
	if err := r.observationReader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, deployment); err != nil {
		return "", "", err
	}
	release := ""
	php := ""
	observed := int32(0)
	validOwners := make(map[types.UID]bool)
	for _, p := range pods.Items {
		if !p.DeletionTimestamp.IsZero() || p.Annotations[workload.ConfigHash] != b.Hash || p.Annotations[workload.SecretRevision] != revision {
			continue
		}
		if !readyReleasePod(&p, image, c.Spec.Runtime.Web.PHP.Image) {
			continue
		}
		owner := metav1.GetControllerOf(&p)
		if owner == nil || owner.Kind != "ReplicaSet" || owner.APIVersion != appsv1.SchemeGroupVersion.String() {
			continue
		}
		// All Pods of a ReplicaSet share its ownership chain. Fetch it once.
		valid, checked := validOwners[owner.UID]
		if !checked {
			rs := &appsv1.ReplicaSet{}
			if err := r.observationReader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: owner.Name}, rs); err != nil {
				if !apierrors.IsNotFound(err) {
					return "", "", err
				}
			} else {
				parent := metav1.GetControllerOf(rs)
				valid = rs.UID == owner.UID && parent != nil && parent.UID == deployment.UID &&
					parent.Kind == deploymentKind && parent.APIVersion == appsv1.SchemeGroupVersion.String()
			}
			validOwners[owner.UID] = valid
		}
		if !valid {
			continue
		}
		observed++
		foundRelease := releaseDigest(&p)
		foundPHP := containerDigest(&p, phpContainer)
		if foundRelease == "" || foundPHP == "" {
			return "", "", nil
		}
		if (release != "" && release != foundRelease) || (php != "" && php != foundPHP) {
			return "", "", nil
		}
		release = foundRelease
		php = foundPHP
	}
	if observed < 1 || observed != webDesiredReplicas(c, deployment) {
		return "", "", nil
	}
	return release, php, nil
}

func readyReleasePod(p *corev1.Pod, image, phpImage string) bool {
	ready := false
	release := false
	php := false
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			ready = true
		}
	}
	for _, container := range p.Spec.InitContainers {
		if container.Name == "release" && container.Image == image {
			release = true
		}
	}
	for _, container := range p.Spec.Containers {
		if container.Name == phpContainer && container.Image == phpImage {
			php = true
		}
	}
	return ready && release && php
}

func controlledJob(c *bx.BitrixCluster, j *batchv1.Job) bool {
	return controlledByCluster(c, j)
}

func (r *BitrixClusterReconciler) deleteValidationJob(ctx context.Context, j *batchv1.Job) error {
	uid, rv := j.UID, j.ResourceVersion
	return client.IgnoreNotFound(r.Delete(ctx, j, client.PropagationPolicy(metav1.DeletePropagationForeground), client.Preconditions{UID: &uid, ResourceVersion: &rv}))
}

// Clear the evidence on the stage result, not on the shared cluster.
func clearSiteEvidence(c *bx.BitrixCluster, observed *ComponentResult) {
	for i := range observed.SitesFor(c.Status.Sites) {
		observed.Sites[i].Exists = false
		observed.Sites[i].Message = "Site existence has not been confirmed for the current runtime"
	}
}

func siteProof(c *bx.BitrixCluster, observed *ComponentResult, identity string, now time.Time) *bx.SiteValidationStatus {
	proof := c.Status.SiteValidation
	if proof == nil || proof.Identity != identity || proof.JobUID == "" ||
		proof.ValidatedAt.IsZero() || proof.ValidatedAt.After(now.Add(time.Minute)) {
		observed.SiteValidationChanged = true
		observed.SiteValidation = nil
		return nil
	}
	return proof
}

// Successful proof remains authoritative during refresh until its bounded
// maximum age, unless the current stage or a previous retry reports failure.
func retainSiteEvidence(c *bx.BitrixCluster, observed *ComponentResult, proof *bx.SiteValidationStatus, now time.Time) {
	current := meta.FindStatusCondition(observed.Conditions, conditionSitesValidated)
	if current == nil {
		current = meta.FindStatusCondition(c.Status.Conditions, conditionSitesValidated)
	}
	if proof == nil {
		observed.SetCondition(c.Generation, conditionSitesValidated, metav1.ConditionUnknown, "ValidationPending", "Waiting for the current site validation Job")
		return
	}
	if !now.Before(proof.ValidatedAt.Add(siteValidationMaxStaleness)) {
		clearSiteEvidence(c, observed)
		observed.SetCondition(c.Generation, conditionSitesValidated, metav1.ConditionUnknown, "ValidationStale", "The last successful site validation has expired; waiting for a fresh result")
		return
	}
	if current != nil && current.Status == metav1.ConditionFalse {
		return
	}
	observed.SetCondition(c.Generation, conditionSitesValidated, metav1.ConditionTrue, sitesValidated, "Last successful site validation remains valid during background refresh")
}

func siteValidationCompletedAt(job *batchv1.Job) metav1.Time {
	if job.Status.CompletionTime != nil && !job.Status.CompletionTime.IsZero() {
		return *job.Status.CompletionTime
	}
	for _, entry := range job.Status.Conditions {
		if entry.Status == corev1.ConditionTrue && (entry.Type == batchv1.JobComplete || entry.Type == batchv1.JobFailed) &&
			!entry.LastTransitionTime.IsZero() {
			return entry.LastTransitionTime
		}
	}
	return job.CreationTimestamp
}

func (r *BitrixClusterReconciler) removeOldSiteValidationJobs(ctx context.Context, c *bx.BitrixCluster, name string, jobs *batchv1.JobList) (bool, error) {
	for i := range jobs.Items {
		j := &jobs.Items[i]
		if j.Name == name || !controlledJob(c, j) {
			continue
		}
		if j.DeletionTimestamp.IsZero() {
			if err := r.deleteValidationJob(ctx, j); err != nil {
				return true, err
			}
		}
		return true, nil
	}
	return false, nil
}

func (r *BitrixClusterReconciler) reconcileSites(ctx context.Context, c *bx.BitrixCluster, b *render.Bundle, revision, image string) (ComponentResult, error) {
	result := ComponentResult{}
	if !meta.IsStatusConditionTrue(c.Status.Conditions, conditionWebReady) {
		result.SiteValidation = nil
		result.SiteValidationChanged = true
		clearSiteEvidence(c, &result)
		result.SetCondition(c.Generation, conditionSitesValidated, metav1.ConditionUnknown, "RuntimeUnresolved", "Waiting for a ready web runtime before site validation")
		return result, nil
	}
	release, php, err := r.observedRuntimeDigests(ctx, c, b, revision, image)
	if err != nil {
		return result, err
	}
	if release == "" || php == "" {
		result.SiteValidation = nil
		result.SiteValidationChanged = true
		clearSiteEvidence(c, &result)
		result.SetCondition(c.Generation, conditionSitesValidated, metav1.ConditionUnknown, "RuntimeUnresolved", "Waiting for consistent release and PHP image digests in ready web Pods")
		return result, nil
	}
	result.Release = &bx.ReleaseStatus{
		Repository: c.Spec.Release.Image.Repository,
		Tag:        c.Spec.Release.Image.Tag,
		Digest:     release,
	}
	result.ReleaseChanged = true
	name := workload.SiteValidationName(c, b, revision, release, php)
	now := time.Now()
	proof := siteProof(c, &result, name, now)
	if proof == nil {
		clearSiteEvidence(c, &result)
		result.SetCondition(c.Generation, conditionSitesValidated, metav1.ConditionUnknown, "ValidationPending", "Waiting for the validation result of the current runtime")
	}
	jobs := &batchv1.JobList{}
	if err = r.reader().List(ctx, jobs, client.InNamespace(c.Namespace), client.MatchingLabels{workload.OwnerUID: string(c.UID), "app.kubernetes.io/name": "bitrix-sites"}); err != nil {
		return result, err
	}
	waiting, err := r.removeOldSiteValidationJobs(ctx, c, name, jobs)
	if err != nil {
		return result, err
	}
	if waiting {
		retainSiteEvidence(c, &result, proof, now)
		// Do not overlap attempts for different runtime identities.
		return result, nil
	}
	requested := make([]string, 0, len(c.Spec.Sites))
	for _, s := range c.Spec.Sites {
		requested = append(requested, s.ID)
	}
	identity := render.ValidationIdentity{ClusterUID: string(c.UID), Generation: c.Generation, Release: release, PHP: php, Attempt: "00000000-0000-0000-0000-000000000000"}
	encoded, _ := json.Marshal(render.SiteResult{Version: render.ContractVersion, Identity: identity, Sites: requested, Reason: sitesValidated})
	if len(encoded) > 4096 {
		result.SetCondition(c.Generation, conditionSitesValidated, metav1.ConditionFalse, "ResultTooLarge", "Requested site IDs exceed the bounded validation result capacity")
		return result, nil
	}

	job := &batchv1.Job{}
	err = r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: name}, job)
	if apierrors.IsNotFound(err) {
		if err = r.resourceManager().Apply(ctx, c, workload.SiteValidation(c, b, revision, image, release, php)); err != nil {
			return result, err
		}
		err = r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: name}, job)
	}
	if err != nil {
		return result, err
	}
	if !controlledJob(c, job) {
		return result, block("ResourceCollision", "Site validation Job is not controlled by this cluster")
	}
	if !job.DeletionTimestamp.IsZero() {
		retainSiteEvidence(c, &result, proof, now)
		return result, nil
	}
	if proof != nil && string(job.UID) == proof.JobUID {
		if now.Sub(proof.ValidatedAt.Time) < siteValidationTTL {
			// The status proof survives loss of the completed Pod and manager restarts.
			// Only the controller can write the status subresource.
			result.SetCondition(c.Generation, conditionSitesValidated, metav1.ConditionTrue, sitesValidated, "All requested site IDs exist in the Bitrix kernel")
			return result, nil
		}
		// Force the current runtime to be revalidated after TTL. A foreground
		// replacement never briefly invalidates an otherwise healthy CR.
		if err = r.deleteValidationJob(ctx, job); err != nil {
			return result, err
		}
		retainSiteEvidence(c, &result, proof, now)
		return result, nil
	}

	identity.Attempt = string(job.UID)
	result, complete, err := r.siteResult(ctx, c, job, identity, requested)
	if err != nil {
		return result, err
	}
	if !complete {
		retainSiteEvidence(c, &result, proof, now)
		return result, nil
	}
	// Keep failed Jobs for five minutes, including failed refresh attempts,
	// so a transient DB/Bitrix failure is retried without reconciling in a loop.
	completedAt := siteValidationCompletedAt(job)
	retryExpired := result.Reason != sitesValidated && time.Since(completedAt.Time) >= 5*time.Minute
	status := metav1.ConditionFalse
	message := "The Bitrix kernel could not validate the requested sites; retrying within five minutes"
	if result.Reason == sitesValidated {
		status = metav1.ConditionTrue
		message = "All requested site IDs exist in the Bitrix kernel"
		result.SiteValidation = &bx.SiteValidationStatus{
			Identity:    name,
			JobUID:      string(job.UID),
			ValidatedAt: completedAt,
		}
		result.SiteValidationChanged = true
	}
	if result.Reason == "SiteNotFound" {
		message = "One or more requested site IDs do not exist in the Bitrix kernel"
	}
	result.SetCondition(c.Generation, conditionSitesValidated, status, result.Reason, message)
	for i := range result.SitesFor(c.Status.Sites) {
		site := &result.Sites[i]
		site.Exists = slices.Contains(result.Sites, site.ID)
		site.Message = "Site existence could not be confirmed"
		if site.Exists {
			site.Message = "Site ID exists in the Bitrix kernel"
		} else if result.Reason == "SiteNotFound" {
			site.Message = "Site ID does not exist in the Bitrix kernel"
		}
	}
	if retryExpired {
		err := r.deleteValidationJob(ctx, job)
		return result, err
	}
	return result, nil
}

func (r *BitrixClusterReconciler) siteResult(ctx context.Context, c *bx.BitrixCluster, job *batchv1.Job, identity render.ValidationIdentity, requested []string) (render.SiteResult, bool, error) {
	unavailable := render.SiteResult{Reason: "ValidationUnavailable"}
	completed := false
	for _, cond := range job.Status.Conditions {
		if cond.Status == corev1.ConditionTrue && cond.Type == batchv1.JobFailed {
			if cond.Reason == "DeadlineExceeded" {
				unavailable.Reason = "ValidationTimeout"
			}
			return unavailable, true, nil
		}
		if cond.Status == corev1.ConditionTrue && cond.Type == batchv1.JobComplete {
			completed = true
		}
	}
	if !completed {
		return unavailable, false, nil
	}
	pods := &corev1.PodList{}
	// The completed Job's result is a security-sensitive, one-shot proof.
	// Read it directly: informer lag must not turn a valid result into a failure.
	if err := r.reader().List(ctx, pods, client.InNamespace(c.Namespace), client.MatchingLabels{"batch.kubernetes.io/controller-uid": string(job.UID)}); err != nil {
		return unavailable, false, err
	}
	// Exactly one successful attempt; no log scraping or pod/exec permissions.
	var result *render.SiteResult
	for _, p := range pods.Items {
		owner := metav1.GetControllerOf(&p)
		if owner == nil || owner.UID != job.UID || owner.Kind != "Job" || owner.APIVersion != batchv1.SchemeGroupVersion.String() || p.Status.Phase != corev1.PodSucceeded || !p.DeletionTimestamp.IsZero() {
			continue
		}
		if releaseDigest(&p) != identity.Release || containerDigest(&p, phpContainer) != identity.PHP {
			return unavailable, true, nil
		}
		for _, s := range p.Status.ContainerStatuses {
			if s.Name != phpContainer || s.State.Terminated == nil || s.State.Terminated.ExitCode != 0 {
				continue
			}
			parsed, err := render.ParseSiteResult([]byte(s.State.Terminated.Message), identity, requested)
			if err != nil || result != nil {
				return unavailable, true, nil
			}
			result = &parsed
		}
	}
	if result == nil {
		return unavailable, true, nil
	}
	return *result, true, nil
}

func summarizeReadiness(c *bx.BitrixCluster) {
	for _, kind := range []string{"StorageReady", conditionWebReady, conditionCronReady, conditionPushReady, conditionSitesValidated, "GatewayReady", "AutoscalingReady"} {
		status := meta.FindStatusCondition(c.Status.Conditions, kind)
		if status == nil || status.Status != metav1.ConditionTrue || status.ObservedGeneration != c.Generation {
			reason, message := "ComponentsPending", "Waiting for required components"
			if status != nil {
				reason, message = status.Reason, status.Message
			}
			condition(c, "Ready", metav1.ConditionFalse, reason, message)
			if kind == "SitesValidated" && status != nil && status.Status == metav1.ConditionFalse {
				c.Status.Phase = bx.BitrixClusterPhaseDegraded
			}
			return
		}
	}
	c.Status.Phase = bx.BitrixClusterPhaseReady
	condition(c, "Ready", metav1.ConditionTrue, "ComponentsReady", "The current runtime and requested Bitrix site IDs are ready")
}
