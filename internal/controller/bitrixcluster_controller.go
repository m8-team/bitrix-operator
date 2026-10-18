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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type BitrixClusterReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	APIReader            client.Reader
	GatewayAPIAvailable  bool
	GatewayParentIndexed bool
	Recorder             events.EventRecorder
}

// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;patch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways;gatewayclasses;referencegrants,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch
// +kubebuilder:rbac:groups=bitrix.m8.team,resources=bitrixclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=bitrix.m8.team,resources=bitrixclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;patch;delete
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
	err := r.reconcileResources(ctx, c)
	gatewayErr := r.reconcileGateway(ctx, c, err == nil)
	result := ctrl.Result{RequeueAfter: 30 * time.Second}
	if gatewayErr != nil {
		issue := classifyReconcileError(gatewayErr, true)
		condition(c, "GatewayReady", metav1.ConditionFalse, issue.reason, issue.message)
		result.RequeueAfter = shorterRequeue(result.RequeueAfter, issue.retryAfter)
		gatewayErr = issue.cause
	}
	if err == nil {
		summarizeReadiness(c)
	} else {
		issue := classifyReconcileError(err, false)
		result.RequeueAfter = shorterRequeue(result.RequeueAfter, issue.retryAfter)
		c.Status.Phase = bx.BitrixClusterPhaseDegraded
		condition(c, "Ready", metav1.ConditionFalse, issue.reason, issue.message)
		condition(c, "WebReady", metav1.ConditionFalse, issue.reason, issue.message)
		if pushEnabled(c) || issue.reason == "PushStopping" {
			condition(c, "PushReady", metav1.ConditionFalse, issue.reason, issue.message)
		}
		if c.Spec.Runtime.Cron.Enabled || issue.reason == "CronStopping" {
			condition(c, "CronReady", metav1.ConditionFalse, issue.reason, issue.message)
		}
		err = issue.cause
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
		r.recordTransition(before, c)
	}
	return result, errors.Join(err, gatewayErr)
}

func validateWorkload(c *bx.BitrixCluster) error {
	s := c.Spec
	if err := validateAutoscaling(c); err != nil {
		return err
	}

	modes := s.Storage.Uploads.PersistentVolumeClaim.AccessModes
	multiPod := s.Runtime.Web.Replicas > 1 || s.Runtime.Cron.Enabled || (workload.AutoscalingEnabled(c) && s.Runtime.Web.Autoscaling.MaxReplicas > 1)
	if slices.Contains(modes, corev1.ReadOnlyMany) {
		return block("InvalidStorageMode", "Uploads storage must be writable")
	}
	if slices.Contains(modes, corev1.ReadWriteOncePod) && (len(modes) != 1 || multiPod) {
		return block("InvalidStorageMode", "ReadWriteOncePod must be the only uploads access mode and can be used only by a single web Pod without cron")
	}
	if multiPod && !slices.Contains(modes, corev1.ReadWriteMany) {
		return block("InvalidStorageMode", "Uploads require ReadWriteMany for multiple web replicas or a separate cron workload")
	}
	if s.Storage.Uploads.PersistentVolumeClaim.Size.Sign() <= 0 || s.Storage.Temporary.SizeLimit.Sign() <= 0 {
		return block("InvalidStorageSize", "Uploads and temporary storage sizes must be positive")
	}
	return nil
}

func deploymentObject() *appsv1.Deployment { return &appsv1.Deployment{} }

func (r *BitrixClusterReconciler) reconcileResources(ctx context.Context, c *bx.BitrixCluster) error {
	if err := r.deleteDisabledRuntime(ctx, c); err != nil {
		return err
	}
	if err := validateWorkload(c); err != nil {
		return err
	}
	b, err := render.RenderWithPort(c.Spec, r.publicationPort(ctx, c), workload.PushEndpoints(c))
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
	if pushEnabled(c) {
		if err = r.reconcilePush(ctx, c, b, revision); err != nil {
			return err
		}
	}
	if err = r.apply(ctx, c, workload.Service(c)); err != nil {
		return err
	}
	if err = r.apply(ctx, c, workload.DisruptionBudget(c)); err != nil {
		return err
	}
	if err = r.apply(ctx, c, workload.Deployment(c, b, revision, image)); err != nil {
		return err
	}
	if c.Spec.Runtime.Cron.Enabled {
		if err = r.reconcileCron(ctx, c, b, revision, image, pvc.Status.Phase == corev1.ClaimBound); err != nil {
			return err
		}
	}
	deployment := deploymentObject()
	if err = r.reader().Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: workload.Name(c, "web")}, deployment); err != nil {
		return err
	}
	if err = r.reconcileHPA(ctx, c); err != nil {
		return err
	}
	desired := webDesiredReplicas(c, deployment)
	rollout := observeDeployment(deployment, desired)
	c.Status.Runtime.Web = rollout.status
	if desired > 0 && rollout.ready && pvc.Status.Phase == corev1.ClaimBound {
		condition(c, "WebReady", metav1.ConditionTrue, "RolloutComplete", "The desired web replicas are ready and uploads storage is bound")
		c.Status.Release = &bx.ReleaseStatus{Repository: c.Spec.Release.Image.Repository, Tag: c.Spec.Release.Image.Tag, Digest: c.Spec.Release.Image.Digest}
	}
	if rollout.deadlineExceeded {
		return block(progressDeadlineExceeded, "The web Deployment exceeded its rollout deadline")
	}
	if err := r.reconcileSites(ctx, c, b, revision, image); err != nil {
		return err
	}
	if rollout.ready && pvc.Status.Phase == corev1.ClaimBound {
		return r.pruneConfigMaps(ctx, c, b)
	}
	return nil
}

func (r *BitrixClusterReconciler) deleteDisabledRuntime(ctx context.Context, c *bx.BitrixCluster) error {
	if !workload.AutoscalingEnabled(c) {
		if err := r.deleteHPA(ctx, c); err != nil {
			return err
		}
	}
	if !c.Spec.Runtime.Cron.Enabled {
		if err := r.deleteCron(ctx, c); err != nil {
			return err
		}
	}
	if !pushEnabled(c) {
		if err := r.deletePush(ctx, c); err != nil {
			return err
		}
	}
	return nil
}
