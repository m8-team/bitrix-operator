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

const sitesValidated = "Validated"

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

func (r *BitrixClusterReconciler) reconcileSites(ctx context.Context, c *bx.BitrixCluster, b *render.Bundle, revision, image string) error {
	if !meta.IsStatusConditionTrue(c.Status.Conditions, "WebReady") {
		return nil
	}
	release, php, err := r.observedRuntimeDigests(ctx, c, b, revision, image)
	if err != nil {
		return err
	}
	if release == "" || php == "" {
		condition(c, "SitesValidated", metav1.ConditionUnknown, "RuntimeUnresolved", "Waiting for consistent release and PHP image digests in ready web Pods")
		return nil
	}
	c.Status.Release = &bx.ReleaseStatus{
		Repository: c.Spec.Release.Image.Repository,
		Tag:        c.Spec.Release.Image.Tag,
		Digest:     release,
	}
	name := workload.SiteValidationName(c, b, revision, release, php)
	jobs := &batchv1.JobList{}
	if err = r.reader().List(ctx, jobs, client.InNamespace(c.Namespace), client.MatchingLabels{workload.OwnerUID: string(c.UID), "app.kubernetes.io/name": "bitrix-sites"}); err != nil {
		return err
	}
	for i := range jobs.Items {
		j := &jobs.Items[i]
		if j.Name != name && controlledJob(c, j) {
			if j.DeletionTimestamp.IsZero() {
				if err = r.deleteValidationJob(ctx, j); err != nil {
					return err
				}
			}
			// Wait for old validation Pods to stop before starting another attempt.
			return nil
		}
	}
	requested := []string{}
	for _, s := range c.Spec.Sites {
		requested = append(requested, s.ID)
	}
	identity := render.ValidationIdentity{ClusterUID: string(c.UID), Generation: c.Generation, Release: release, PHP: php, Attempt: "00000000-0000-0000-0000-000000000000"}
	encoded, _ := json.Marshal(render.SiteResult{Version: render.ContractVersion, Identity: identity, Sites: requested, Reason: sitesValidated})
	if len(encoded) > 4096 {
		condition(c, "SitesValidated", metav1.ConditionFalse, "ResultTooLarge", "Requested site IDs exceed the bounded validation result capacity")
		return nil
	}
	if err = r.apply(ctx, c, workload.SiteValidation(c, b, revision, image, release, php)); err != nil {
		return err
	}
	job := &batchv1.Job{}
	if err = r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: name}, job); err != nil {
		return err
	}
	if !controlledJob(c, job) {
		return block("ResourceCollision", "Site validation Job is not controlled by this cluster")
	}
	identity.Attempt = string(job.UID)
	result, complete, err := r.siteResult(ctx, c, job, identity, requested)
	if err != nil {
		return err
	}
	if !complete {
		return nil
	}
	retryAfter := 5 * time.Minute
	if result.Reason == sitesValidated {
		retryAfter = 15 * time.Minute
	}
	completed := job.CreationTimestamp.Time
	for _, condition := range job.Status.Conditions {
		if condition.Status == corev1.ConditionTrue && (condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed) {
			completed = condition.LastTransitionTime.Time
		}
	}
	if time.Since(completed) >= retryAfter {
		return r.deleteValidationJob(ctx, job)
	}
	status := metav1.ConditionFalse
	message := "The Bitrix kernel could not validate the requested sites; retrying within five minutes"
	if result.Reason == sitesValidated {
		status = metav1.ConditionTrue
		message = "All requested site IDs exist in the Bitrix kernel"
	}
	if result.Reason == "SiteNotFound" {
		message = "One or more requested site IDs do not exist in the Bitrix kernel"
	}
	condition(c, "SitesValidated", status, result.Reason, message)
	for i := range c.Status.Sites {
		site := &c.Status.Sites[i]
		site.Exists = slices.Contains(result.Sites, site.ID)
		site.Message = "Site existence could not be confirmed"
		if site.Exists {
			site.Message = "Site ID exists in the Bitrix kernel"
		} else if result.Reason == "SiteNotFound" {
			site.Message = "Site ID does not exist in the Bitrix kernel"
		}
	}
	return nil
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
	if err := r.observationReader().List(ctx, pods, client.InNamespace(c.Namespace), client.MatchingLabels{"batch.kubernetes.io/controller-uid": string(job.UID)}); err != nil {
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
	for _, kind := range []string{"StorageReady", "WebReady", "CronReady", "PushReady", "SitesValidated", "GatewayReady", "AutoscalingReady"} {
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
