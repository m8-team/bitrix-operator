package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	bx "github.com/m8-team/bitrix-operator/api/v1alpha1"
	render "github.com/m8-team/bitrix-operator/internal/runtime"
	"github.com/m8-team/bitrix-operator/internal/workload"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	stagePush               = "push"
	stageWeb                = "web"
	stageCron               = "cron"
	stageWebStatus          = "web-status"
	stageSites              = "sites"
	conditionWebReady       = "WebReady"
	conditionSitesValidated = "SitesValidated"
	conditionPushReady      = "PushReady"
	conditionCronReady      = "CronReady"
)

// runtimeDesiredState describes the configuration we intend to apply. It is
// prepared once and never updated by any reconciliation stage.
type runtimeDesiredState struct {
	bundle   *render.Bundle
	revision string
	image    string
}

// runtimeObservedState is a snapshot of dependencies read from the API. Runtime
// rollout observations are reported via ComponentResult, not mutable inputs.
type runtimeObservedState struct {
	storageBound bool
}

type runtimeInputs struct {
	desired  runtimeDesiredState
	observed runtimeObservedState
}

// runtimeComponent is the narrow interface for one independently testable
// reconciliation step. It is intentionally local to the controller: rendering
// and resource APIs do not depend on this orchestration layer.
type runtimeComponent interface {
	Name() string
	Reconcile(context.Context, *bx.BitrixCluster, *runtimeInputs) (ComponentResult, error)
}

// applyOnlyStep is for stages that only change Kubernetes resources. It emits
// a ComponentResult directly, without capturing a deep copy of the whole CR.
// An accidental status mutation is reverted rather than leaking around
// StatusManager's observation boundary.
type applyOnlyStep struct {
	name string
	run  func(context.Context, *bx.BitrixCluster, *runtimeInputs) error
}

func (s applyOnlyStep) Name() string { return s.name }

func (s applyOnlyStep) Reconcile(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) (ComponentResult, error) {
	before := c.Status.DeepCopy()
	start := time.Now()
	err := s.run(ctx, c, in)
	if !reflect.DeepEqual(c.Status, *before) {
		c.Status = *before
		err = errors.Join(err, fmt.Errorf("apply-only stage %s changed BitrixCluster status", s.name))
	}
	recordStageMetrics(s.name, err, time.Since(start))
	return ComponentResult{Name: s.name, Applied: err == nil}, err
}

// observedStep runs a statusful stage that constructs ComponentResult directly.
// Guard against accidentally bypassing the StatusManager aggregation boundary.
type observedStep struct {
	name string
	run  func(context.Context, *bx.BitrixCluster, *runtimeInputs) (ComponentResult, error)
}

func (s observedStep) Name() string { return s.name }

func (s observedStep) Reconcile(ctx context.Context, c *bx.BitrixCluster, in *runtimeInputs) (ComponentResult, error) {
	before := c.Status.DeepCopy()
	start := time.Now()
	observation, err := s.run(ctx, c, in)
	if !reflect.DeepEqual(c.Status, *before) {
		c.Status = *before
		err = errors.Join(err, fmt.Errorf("observed stage %s mutated the shared BitrixCluster status", s.name))
	}
	recordStageMetrics(s.name, err, time.Since(start))
	observation.Name = s.name
	observation.Applied = err == nil
	return observation, err
}

// runRuntimeComponents adapts the existing isolated components to ReconcileStep.
// Existing stage ordering and short-circuit semantics remain unchanged.
func runRuntimeComponents(ctx context.Context, status *StatusManager, in *runtimeInputs, components []runtimeComponent) ([]ComponentResult, error) {
	c := status.cluster
	steps := make([]ReconcileStep, 0, len(components))
	for _, component := range components {
		steps = append(steps, ReconcileStep{
			Name: component.Name(),
			Run: func(ctx context.Context) (ComponentResult, StepResult, error) {
				observation, err := component.Reconcile(ctx, c, in)
				return observation, StepContinue(), err
			},
		})
	}
	outcome, err := RunReconcileSteps(ctx, status, steps)
	return outcome.Results, err
}

func (r *BitrixClusterReconciler) reconcileResources(ctx context.Context, status *StatusManager) error {
	c := status.cluster
	start := time.Now()
	in, preparation, err := r.prepareRuntime(ctx, c)
	recordStageMetrics("prepare", err, time.Since(start))
	status.Apply(preparation)
	if err != nil {
		return &componentError{name: "prepare", cause: err}
	}
	_, err = runRuntimeComponents(ctx, status, in, r.runtimeComponents())
	return err
}

// Preparation deliberately handles disabling old controllers before examining
// broken Secrets/configuration: this keeps cleanup and SSA transitions safe.
// Each completed cleanup contributes to a direct observation, even if a later
// validation or Kubernetes API call fails.
func (r *BitrixClusterReconciler) prepareRuntime(ctx context.Context, c *bx.BitrixCluster) (*runtimeInputs, ComponentResult, error) {
	result := ComponentResult{Name: "prepare"}
	if err := r.deleteDisabledRuntime(ctx, c, &result); err != nil {
		return nil, result, err
	}
	if err := validateWorkload(c); err != nil {
		return nil, result, err
	}
	port, err := r.publicationPort(ctx, c)
	if err != nil {
		return nil, result, err
	}
	bundle, err := render.RenderWithPort(c.Spec, port, workload.PushEndpoints(c))
	if err != nil {
		return nil, result, block("InvalidRuntimeConfiguration", err.Error())
	}
	image, err := render.ReleaseReference(c.Spec.Release.Image)
	if err != nil {
		return nil, result, block("InvalidRelease", err.Error())
	}
	revision, err := r.secrets(ctx, c, bundle)
	if err != nil {
		return nil, result, err
	}
	pvc, err := r.resourceManager().Storage(ctx, c)
	if err != nil {
		return nil, result, err
	}
	bound := pvc.Status.Phase == corev1.ClaimBound
	if bound {
		result.SetCondition(c.Generation, "StorageReady", metav1.ConditionTrue, "VolumeBound", "Uploads PVC is bound")
	} else {
		result.SetCondition(c.Generation, "StorageReady", metav1.ConditionFalse, "VolumePending", "Waiting for the uploads PVC to bind")
	}
	return &runtimeInputs{
		desired: runtimeDesiredState{bundle: bundle, revision: revision, image: image},
		observed: runtimeObservedState{storageBound: bound},
	}, result, nil
}

func (r *BitrixClusterReconciler) runtimeComponents() []runtimeComponent {
	return []runtimeComponent{
		applyOnlyStep{name: "config", run: r.reconcileRuntimeConfig},
		observedStep{name: stagePush, run: r.reconcileRuntimePush},
		applyOnlyStep{name: stageWeb, run: r.reconcileRuntimeWeb},
		observedStep{name: stageCron, run: r.reconcileRuntimeCron},
		observedStep{name: stageWebStatus, run: r.observeRuntimeWeb},
		observedStep{name: stageSites, run: r.reconcileRuntimeSites},
		applyOnlyStep{name: "config-gc", run: r.reconcileRuntimeConfigGC},
	}
}

func (r *BitrixClusterReconciler) deleteDisabledRuntime(ctx context.Context, c *bx.BitrixCluster, result *ComponentResult) error {
	if !workload.AutoscalingEnabled(c) {
		if err := r.deleteHPA(ctx, c); err != nil {
			return err
		}
		result.SetCondition(c.Generation, "AutoscalingReady", metav1.ConditionTrue, "Disabled", "Web autoscaling is disabled")
	}
	if !c.Spec.Runtime.Cron.Enabled {
		if err := r.deleteCron(ctx, c); err != nil {
			return err
		}
		result.RuntimeFor(c.Status.Runtime).Cron = bx.WorkloadStatus{}
		result.SetCondition(c.Generation, conditionCronReady, metav1.ConditionTrue, "Disabled", "Cron workload is disabled")
	}
	if !pushEnabled(c) {
		if err := r.deletePush(ctx, c, result); err != nil {
			return err
		}
		result.RuntimeFor(c.Status.Runtime).PushPublisher = bx.WorkloadStatus{}
		result.RuntimeFor(c.Status.Runtime).PushSubscriber = bx.WorkloadStatus{}
		result.SetCondition(c.Generation, conditionPushReady, metav1.ConditionTrue, "Disabled", "Push workloads are disabled")
	}
	return nil
}
