/*
Copyright 2026 M8 Team.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type BitrixClusterReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	APIReader client.Reader
}

// +kubebuilder:rbac:groups=bitrix.m8.team,resources=bitrixclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=bitrix.m8.team,resources=bitrixclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups="",resources=services;configmaps,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch

func condition(c *bx.BitrixCluster, kind string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&c.Status.Conditions, metav1.Condition{Type: kind, Status: status, Reason: reason, Message: message, ObservedGeneration: c.Generation})
}

// Reconcile converges the application resources and reports observed rollout state.
func (r *BitrixClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	c := &bx.BitrixCluster{}
	if err := r.Get(ctx, req.NamespacedName, c); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !c.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	before := c.DeepCopy()
	c.Status.ObservedGeneration = c.Generation
	c.Status.Phase = bx.BitrixClusterPhaseProgressing
	c.Status.Release = nil
	c.Status.Runtime = &bx.RuntimeStatus{Web: bx.WorkloadStatus{DesiredReplicas: c.Spec.Runtime.Web.Replicas}}
	c.Status.Sites = nil
	for _, site := range c.Spec.Sites {
		c.Status.Sites = append(c.Status.Sites, bx.BitrixSiteStatus{ID: site.ID, Message: "Site existence has not been validated through the Bitrix kernel"})
	}
	condition(c, "Ready", metav1.ConditionFalse, "SiteValidationPending", "Web readiness does not validate Bitrix site IDs; kernel validation is pending")
	condition(c, "SitesValidated", metav1.ConditionUnknown, "NotImplemented", "Kernel site validation will be implemented in a subsequent iteration")
	condition(c, "WebReady", metav1.ConditionFalse, "RolloutPending", "Waiting for the desired web rollout")
	condition(c, "StorageReady", metav1.ConditionUnknown, "NotObserved", "Uploads storage has not been observed")
	err := r.reconcileResources(ctx, c)
	result := ctrl.Result{}
	if err != nil {
		reason, message := "ReconcileFailed", "Could not reconcile application resources; see controller logs"
		if issue, ok := errors.AsType[*blocked](err); ok {
			reason, message = issue.reason, issue.message
			err = nil
			result.RequeueAfter = time.Minute
		}
		if apierrors.IsConflict(err) {
			ctrl.LoggerFrom(ctx).Info("Resource apply conflicted", "error", err)
			reason, message = "ApplyConflict", "A resource changed or an owned field has a conflicting manager"
			err = nil
			result.RequeueAfter = 10 * time.Second
		}
		c.Status.Phase = bx.BitrixClusterPhaseDegraded
		condition(c, "Ready", metav1.ConditionFalse, reason, message)
		condition(c, "WebReady", metav1.ConditionFalse, reason, message)
	}
	// Conditions are evaluated provisionally above. Preserve transition times for unchanged final states.
	for i := range c.Status.Conditions {
		previous := meta.FindStatusCondition(before.Status.Conditions, c.Status.Conditions[i].Type)
		if previous != nil && previous.Status == c.Status.Conditions[i].Status {
			c.Status.Conditions[i].LastTransitionTime = previous.LastTransitionTime
		}
	}
	if !reflect.DeepEqual(before.Status, c.Status) {
		if patchErr := r.Status().Patch(ctx, c, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
	}
	return result, err
}

func validateWorkload(c *bx.BitrixCluster) error {
	s := c.Spec
	if s.Runtime.Cron.Enabled || (s.Runtime.Cron.Agents != nil && s.Runtime.Cron.Agents.Enabled) ||
		(s.Runtime.Push != nil && s.Runtime.Push.Enabled) || (s.Runtime.Web.Autoscaling != nil && s.Runtime.Web.Autoscaling.Enabled) ||
		(s.Networking != nil && s.Networking.Gateway != nil && s.Networking.Gateway.Enabled) {
		return block("UnsupportedFeatures", "Cron, agents, Push, autoscaling and Gateway publication are not implemented yet")
	}
	for _, site := range s.Sites {
		if site.TLS != nil {
			return block("UnsupportedFeatures", "TLS publication is not implemented yet")
		}
	}
	modes := s.Storage.Uploads.PersistentVolumeClaim.AccessModes
	if slices.Contains(modes, corev1.ReadOnlyMany) || (s.Runtime.Web.Replicas > 1 && !slices.Contains(modes, corev1.ReadWriteMany)) {
		return block("InvalidStorageMode", "Uploads require writable storage and ReadWriteMany for multiple replicas")
	}
	if s.Storage.Uploads.PersistentVolumeClaim.Size.Sign() <= 0 || s.Storage.Temporary.SizeLimit.Sign() <= 0 {
		return block("InvalidStorageSize", "Uploads and temporary storage sizes must be positive")
	}
	return nil
}

func deploymentObject() *appsv1.Deployment { return &appsv1.Deployment{} }

func (r *BitrixClusterReconciler) reconcileResources(ctx context.Context, c *bx.BitrixCluster) error {
	if err := validateWorkload(c); err != nil {
		return err
	}
	b, err := render.Render(c.Spec)
	if err != nil {
		return block("InvalidRuntimeConfiguration", err.Error())
	}
	image, err := render.ReleaseReference(c.Spec.Release.Image)
	if err != nil {
		return block("InvalidRelease", err.Error())
	}
	revision, err := r.secrets(ctx, c, b)
	if err != nil {
		return err
	}
	pvc, err := r.storage(ctx, c)
	if err != nil {
		return err
	}
	condition(c, "StorageReady", metav1.ConditionFalse, "VolumePending", "Waiting for the uploads PVC to bind")
	if pvc.Status.Phase == corev1.ClaimBound {
		condition(c, "StorageReady", metav1.ConditionTrue, "VolumeBound", "Uploads PVC is bound")
	}
	for _, configuration := range workload.ConfigMaps(c, b) {
		if err = r.apply(ctx, c, configuration); err != nil {
			return err
		}
	}
	if err = r.apply(ctx, c, workload.Service(c)); err != nil {
		return err
	}
	if err = r.apply(ctx, c, workload.Deployment(c, b, revision, image)); err != nil {
		return err
	}
	deployment := deploymentObject()
	if err = r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, deployment); err != nil {
		return err
	}
	desired := c.Spec.Runtime.Web.Replicas
	d := deployment.Status
	c.Status.Runtime.Web = bx.WorkloadStatus{DesiredReplicas: desired, ReadyReplicas: d.ReadyReplicas, UpdatedReplicas: d.UpdatedReplicas, AvailableReplicas: d.AvailableReplicas}
	if d.ObservedGeneration >= deployment.Generation && d.UpdatedReplicas == desired && d.ReadyReplicas == desired && d.AvailableReplicas == desired && d.Replicas == desired && pvc.Status.Phase == corev1.ClaimBound {
		condition(c, "WebReady", metav1.ConditionTrue, "RolloutComplete", "The desired web replicas are ready and uploads storage is bound")
		c.Status.Release = &bx.ReleaseStatus{Repository: c.Spec.Release.Image.Repository, Tag: c.Spec.Release.Image.Tag, Digest: c.Spec.Release.Image.Digest}
	}
	for _, status := range d.Conditions {
		if status.Type == appsv1.DeploymentProgressing && status.Status == corev1.ConditionFalse && status.Reason == "ProgressDeadlineExceeded" {
			return block("ProgressDeadlineExceeded", "The web Deployment exceeded its rollout deadline")
		}
	}
	return nil
}
